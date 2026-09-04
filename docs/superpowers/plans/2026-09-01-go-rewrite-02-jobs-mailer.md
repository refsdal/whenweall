# Go Rewrite Plan 2/8 — Jobs & Mailer

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The `scheduled_jobs` poller and the SMTP mail pipeline: enqueue → claim (`FOR UPDATE SKIP LOCKED`) → render `html/template` → deliver via go-mail, with backoff, dead-lettering, and a Mailpit-verified send.

**Architecture:** `internal/jobs` is a faithful port of `src/server/jobs/scheduler.ts` — **read that file first**; its SQL (claim CTE, backoff CASE, upsert-on-(kind,room_key)) is copied nearly verbatim, and its doc comments carry over. `internal/mailer` renders templates and speaks SMTP; mail is *always* queued, never sent from a request handler.

**Tech Stack:** `wneessen/go-mail`, `html/template` + `embed`, stdlib elsewhere.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§5)

## Global Constraints

See Plan 1's Global Constraints — they apply verbatim. Additionally:
- Job payload privacy rule (from `src/server/mailer/queue.ts`, keep its rationale comment): entity mails (booking/poll notifications) enqueue **ids only** and re-read at send time; auth/org mails (verification, reset, magic link, invitation) must carry `{to, template, data}` directly because their tokens cannot be re-derived — cap and never log payloads.
- Mail jobs use `max_attempts = 10` (spec §5); other kinds default to 5.

---

### Task 1: internal/jobs — schedule, claim, complete, fail

**Files:**
- Create: `internal/jobs/jobs.go`
- Test: `internal/jobs/jobs_test.go`

**Interfaces:**
- Consumes: `db.DBTX`, `db.NewID()`, `testdb.New(t)` from plan 1.
- Produces:

```go
package jobs

type Job struct {
    ID          string
    Kind        string
    RoomKey     *string
    RunAt       time.Time
    Payload     json.RawMessage // nil when none
    Attempts    int
    MaxAttempts int
}

type ScheduleInput struct {
    Kind        string
    RoomKey     *string // nil = independent job (each queued mail); non-nil = upsert per room
    RunAt       time.Time
    Payload     any // JSON-marshalled; nil allowed
    MaxAttempts int // 0 → 5
}

func Schedule(ctx context.Context, tx db.DBTX, in ScheduleInput) error
func Cancel(ctx context.Context, tx db.DBTX, kind, roomKey string) error
func ClaimDue(ctx context.Context, tx db.DBTX, replicaID string, limit int) ([]Job, error)
func Complete(ctx context.Context, tx db.DBTX, id string) error
func Fail(ctx context.Context, tx db.DBTX, id, errMsg string) (willRetry bool, err error)
func Dead(ctx context.Context, tx db.DBTX, limit int) ([]Job, error)     // attempts >= max_attempts
func Retry(ctx context.Context, tx db.DBTX, id string) error             // admin console: attempts=0, run_at=now()
```

- Port each function's SQL and doc comment from `src/server/jobs/scheduler.ts` (LOCK_TIMEOUT 5 min, `JOBS_CHANNEL = "whenweall_jobs"` pg_notify after schedule, backoff `LEAST(power(2, attempts) * interval '1 minute', interval '1 hour')`, `last_error` capped at 2000 chars, upsert resets attempts/locks). `Retry` is new (needed by plan 7): `UPDATE scheduled_jobs SET attempts = 0, locked_by = NULL, locked_at = NULL, last_error = NULL, run_at = now() WHERE id = $1`.

- [ ] **Step 1: Write the failing tests**

```go
package jobs_test // uses testdb.New(t); ctx := context.Background() throughout

func TestScheduleAndClaim(t *testing.T) {
    d := testdb.New(t)
    err := jobs.Schedule(ctx, d, jobs.ScheduleInput{Kind: "mail:send", RunAt: time.Now().Add(-time.Second), Payload: map[string]string{"to": "a@b.c"}})
    // claim
    claimed, err := jobs.ClaimDue(ctx, d, "replica-1", 10)
    // assert: len==1, Attempts==1, Payload contains "a@b.c"
}

func TestClaimSkipsFutureJobs(t *testing.T)        // RunAt in +1h → ClaimDue returns empty
func TestTwoWorkersGetDisjointSets(t *testing.T)   // schedule 4 due jobs; ClaimDue("w1",2) + ClaimDue("w2",10) → 2+2, no shared IDs
func TestRoomKeyUpsertKeepsOneJob(t *testing.T)    // Schedule(kind,room) twice → SELECT count(*)==1, run_at is the second value, attempts reset to 0
func TestFailBacksOffThenDeadLetters(t *testing.T) {
    // maxAttempts 2. claim → Fail → willRetry true, run_at pushed to future; force run_at back to past via UPDATE;
    // claim (attempts→2) → Fail → willRetry false; Dead(ctx,d,10) contains it; ClaimDue no longer returns it.
}
func TestCompleteDeletesRow(t *testing.T)
func TestLockTimeoutReclaims(t *testing.T) {
    // claim with w1, then UPDATE locked_at = now() - interval '6 minutes'; ClaimDue("w2",1) reclaims it (attempts==2)
}
func TestRetryResurrectsDeadJob(t *testing.T)      // dead job + Retry → claimable again, last_error NULL
```

Write these fully (each is straightforward SQL assertions on the table); they are the contract for the poller and for plan 7's dead-letter screen.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/jobs/` → FAIL (undefined).

- [ ] **Step 3: Implement `internal/jobs/jobs.go`** — direct SQL translation of `scheduler.ts` as specified in Interfaces. No sqlc here: the claim CTE and upsert are the kind of SQL sqlc handles poorly and there are only seven statements.

- [ ] **Step 4: Run tests** — `go test ./internal/jobs/ -v` → all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs
git commit -m "feat(jobs): scheduled_jobs port — claim/backoff/dead-letter semantics from scheduler.ts"
```

---

### Task 2: internal/jobs — the worker loop

**Files:**
- Create: `internal/jobs/worker.go`
- Test: `internal/jobs/worker_test.go`

**Interfaces:**
- Produces:

```go
type Handler func(ctx context.Context, job Job) error

type Worker struct { /* db, replicaID, handlers map[string]Handler, log *slog.Logger, PollInterval time.Duration */ }

func NewWorker(sqlDB *sql.DB, replicaID string, log *slog.Logger) *Worker // PollInterval defaults 5s
func (w *Worker) Register(kind string, h Handler)
func (w *Worker) RunOnce(ctx context.Context) (processed int, err error) // test seam + loop body
func (w *Worker) Run(ctx context.Context)                                // ticker loop until ctx done
```

- Behavior: `RunOnce` claims up to 20 jobs, runs each handler; handler nil-error → `Complete`; error → `Fail` (log at WARN with kind+id+attempts, ERROR when `willRetry == false` — the dead-letter moment must be loud). A panic in a handler is recovered and treated as an error. Unknown kind → `Fail` with "no handler registered". `Run` also LISTENs? No — keep plan 2 poll-only (5 s); the NOTIFY wake-up latency optimisation can ride on the rooms listener in plan 6 if wanted, and `Schedule` already emits it.

- [ ] **Step 1: Write the failing tests**

```go
func TestWorkerProcessesJob(t *testing.T)      // Register "t:ok" handler appending to a channel; Schedule; RunOnce → processed 1, row gone
func TestWorkerFailureSchedulesRetry(t *testing.T) // handler returns error → row remains, attempts=1, last_error set
func TestWorkerRecoversPanic(t *testing.T)     // handler panics → RunOnce returns nil error, job failed not lost
func TestWorkerUnknownKindDeadLettersEventually(t *testing.T)
```

- [ ] **Step 2: Run to verify failure**, **Step 3: implement**, **Step 4: run to green** (same commands as Task 1).

- [ ] **Step 5: Wire into serve()**

In `cmd/whenweall/main.go` `serve()`: create `jobs.NewWorker(db, hostname+"-"+db.NewID()[:6], slog.Default())` and `go worker.Run(ctx)` before `ListenAndServe`. (Handlers get registered by later plans; an empty worker is harmless.)

- [ ] **Step 6: Commit** — `git commit -m "feat(jobs): polling worker with per-kind handlers"`

---

### Task 3: internal/mailer — templates and rendering

Re-author the React Email templates (`emails/*.tsx` — read `_Layout.tsx` first, then each template you port) as Go `html/template`. Check `emails/` and `src/server/mailer/templates.tsx` for whether copy is paraglide-localized; port what exists — if templates are English-only, stay English-only (spec §5).

**Files:**
- Create: `internal/mailer/render.go`, `internal/mailer/templates/layout.html`, and one `internal/mailer/templates/<name>.html` + `<name>.txt` per template below
- Test: `internal/mailer/render_test.go`

Templates to port (name → source):
`verify_email` ← `emails/VerifyEmail.tsx` · `reset_password` ← `ResetPassword.tsx` · `magic_link` (new — copy modelled on VerifyEmail: "sign in with this link") · `org_invite` ← `OrgInvite.tsx` · `finalized` ← `Finalized.tsx` · `closed` ← `Closed.tsx` · `digest` ← `Digest.tsx` · `notification` ← `Notification.tsx` · `claim_confirmation` ← `ClaimConfirmation.tsx` · `booking_confirmed` ← `BookingConfirmed.tsx` · `booking_cancelled` ← `BookingCancelled.tsx` · `booking_rescheduled` ← `BookingRescheduled.tsx` · `booking_rescheduled_organiser` ← `BookingRescheduledOrganiser.tsx` · `booking_organiser_notice` ← `BookingOrganiserNotice.tsx` · `booking_reminder` ← `BookingReminder.tsx` · `booking_sync_failed` ← `BookingSyncFailed.tsx`

**Interfaces:**
- Produces:

```go
package mailer

type Rendered struct { Subject, HTML, Text string }

// Render executes templates/<name>.html and .txt inside layout.html.
// data is template-specific; every template can use .AppURL. Unknown name → error.
func Render(name string, data map[string]any) (Rendered, error)

type Attachment struct { Filename, ContentType string; Content []byte }

type Message struct {
    To, ToName  string
    Template    string
    Data        map[string]any
    Attachments []Attachment // .ics files, added by plans 4/5
}
```

- Subjects live in the templates as a `{{define "subject"}}…{{end}}` block so copy stays in one file per mail.

- [ ] **Step 1: Write the failing tests**

```go
func TestRenderAllTemplates(t *testing.T) {
    // table over every template name with minimal plausible data;
    // assert: no error, HTML contains the layout footer marker, Text non-empty,
    // Subject non-empty, and HTML escapes data (inject "<script>" via a data field, assert "&lt;script&gt;").
}
func TestRenderUnknownTemplateErrors(t *testing.T)
```

- [ ] **Step 2: Run to verify failure.**

- [ ] **Step 3: Implement** — `//go:embed templates` FS, parse once at init into `*template.Template` per name (html) + `text/template` for `.txt`. Layout provides header/footer and a `content` block; port the visual shell of `emails/_Layout.tsx` as inline-styled table HTML (email clients: inline styles only, no external CSS). Text parts are new: a short plain rendition per template (greeting, the key line, the link).

- [ ] **Step 4: Run to green, then commit** — `git commit -m "feat(mailer): go templates for all transactional mail, with plain-text parts"`

---

### Task 4: internal/mailer — SMTP transport + queue integration

**Files:**
- Create: `internal/mailer/mailer.go`
- Test: `internal/mailer/mailer_test.go` (unit: from-address parsing, job payload round-trip), `internal/mailer/smtp_test.go` (integration against Mailpit)

**Interfaces:**
- Consumes: `jobs.Schedule/Register`, `config.Config`, Task 3 `Render`/`Message`.
- Produces:

```go
// New builds the transport from config (host/port/user/password/secure, EMAIL_FROM).
func New(cfg *config.Config) *Mailer
// Send renders and delivers immediately. Only the job handler and tests call this.
func (m *Mailer) Send(ctx context.Context, msg Message) error
// Enqueue schedules kind "mail:send" with the Message as payload (MaxAttempts 10).
// This is the only send API request handlers may use.
func Enqueue(ctx context.Context, tx db.DBTX, msg Message) error
// RegisterHandler wires kind "mail:send" into the worker: unmarshal Message → m.Send.
func (m *Mailer) RegisterHandler(w *jobs.Worker)
// ParseFromAddress ports parseFromAddress from src/server/mailer/mailer.ts (keep its tests' cases).
func ParseFromAddress(v string) (name, email string)
```

- go-mail wiring: `gomail.NewClient(host, gomail.WithPort(port), gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover), gomail.WithUsername(...), gomail.WithPassword(...))`; `SMTPSecure=true` → `gomail.WithSSLPort(false)`, else opportunistic STARTTLS (`gomail.WithTLSPolicy(gomail.TLSOpportunistic)`); skip auth entirely when user+password are empty (Mailpit/localhost relays). Body: `SetBodyString(TypeTextPlain, text)` + `AddAlternativeString(TypeTextHTML, html)`; attachments via `AttachReader` with content type.

- [ ] **Step 1: Failing unit tests** — `ParseFromAddress` table ported from the TS tests (`"whenweall <a@b>"` → name+addr, bare addr → addr, quoted name unquoted); `Enqueue`-then-claim round-trip: payload unmarshals back to the same `Message` (use `testdb`).

- [ ] **Step 2: Failing integration test** `smtp_test.go` — start Mailpit via testcontainers:

```go
// axllent/mailpit: SMTP :1025, HTTP API :8025.
// Send a message with HTML+text+one .ics attachment, then GET /api/v1/messages
// and assert: 1 message, correct To/Subject, and GET /api/v1/message/{id} shows 2 body parts + attachment.
```

- [ ] **Step 3: Implement**, **Step 4: run to green** — `go test ./internal/mailer/ -v`.

- [ ] **Step 5: Wire into serve():** construct `mailer.New(cfg)`, call `m.RegisterHandler(worker)`.

- [ ] **Step 6: Commit** — `git commit -m "feat(mailer): go-mail SMTP transport behind the job queue"`

---

### Task 5: Housekeeping jobs

**Files:**
- Create: `internal/jobs/housekeeping.go`
- Test: `internal/jobs/housekeeping_test.go`

**Interfaces:**
- Produces: `RegisterHousekeeping(w *Worker, sqlDB *sql.DB)` registering:
  - `"rooms:prune"` — `DELETE FROM room_events WHERE created_at < now() - interval '1 hour'`, then reschedule itself (`Schedule` with RoomKey `"global"` — the upsert makes it a singleton) for +10 min.
  - `"presence:sweep"` — `DELETE FROM ws_presence WHERE heartbeat_at < now() - interval '90 seconds'`; reschedule +1 min.
  - `"ratelimit:sweep"` — `DELETE FROM rate_limits WHERE reset_at < now() - interval '1 hour'`; reschedule +1 h.
  - `EnsureScheduled(ctx, db)` — called from serve(): schedules all three if absent (the upsert makes boot idempotent across replicas).

- [ ] **Step 1: Failing tests** — insert an old + a fresh `room_events` row, run the handler via `RunOnce`, assert only the fresh row remains and a future `rooms:prune` job exists again. Same shape for the other two.
- [ ] **Step 2: Implement, run to green.**
- [ ] **Step 3: Wire `EnsureScheduled` into serve(). Commit** — `git commit -m "feat(jobs): self-rescheduling housekeeping (room_events, presence, rate_limits)"`
