# Go Rewrite Plan 4/8 — Polls & Sign-up Sheets API

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The complete scheduling-poll + sign-up-sheet JSON API: CRUD, voting, comments, claims with `FOR UPDATE` atomicity (plus the racing-goroutines proof), `.ics`, notifications mail, poll timers, and Turnstile on the public endpoints.

**Architecture:** `internal/polls` is a behavioral port of `src/server/polls/*` — **each task names its TS source; read it before implementing, port its doc comments where they explain a rule**. Handlers speak the `/api/v1` envelope; domain writes that must reach live viewers call `rooms.Emit` (a pure DB write defined here; the hub that fans out arrives in plan 6). sqlc generates the query layer.

**Tech Stack:** sqlc (`database/sql` mode), stdlib.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§2, §4-atomicity, §5)

## Global Constraints

Plan 1's Global Constraints apply. Additionally:
- Error envelope everywhere: `{"error":{"code":"...","message":"..."}}`. Codes used by the frontend: `unauthenticated`, `forbidden`, `not_found`, `invalid` (with `fields` map), `conflict`, `capacity_full`, `rate_limited`, `captcha_failed`, `internal`.
- Public (unauthenticated) mutating endpoints verify Turnstile when `cfg.Capabilities.Turnstile` (Task 6).
- Every domain mutation that a live viewer must see calls `rooms.Emit` **in the same transaction** (spec §4).

---

### Task 1: Domain migration 00003 + sqlc setup

**Files:**
- Create: `migrations/00003_polls.sql`, `sqlc.yaml`, `internal/polls/queries/polls.sql`, `internal/rooms/emit.go`
- Test: extend `internal/db/db_test.go` table list; `internal/rooms/emit_test.go`.

**Interfaces:**
- Produces: tables `polls`, `poll_options`, `participants`, `votes`, `comments`, `notification_prefs`, `notification_subscriptions`; the sqlc package `internal/polls/queries` (`queries.New(db.DBTX)`); and the event-write half of the rooms design (the listening hub arrives in plan 6):

```go
package rooms

// Emit appends one event to room_events and notifies the "room_events" channel with
// "<roomKey>:<id>" — a pointer, never a payload (spec §4). Call it INSIDE the same
// transaction as the domain write; NOTIFY is transactional, so it fires only on commit.
func Emit(ctx context.Context, tx db.DBTX, roomKey, eventType string, data any) error
// event column shape: {"type": eventType, "data": data} — seq is the bigserial id.
```

`emit_test.go`: Emit inside a rolled-back tx leaves no row; inside a committed tx the row exists with the right jsonb shape, and a raw `LISTEN room_events` connection (open one with pgx on `testdb.URL`) receives `<roomKey>:<id>` after commit and nothing before.

- [ ] **Step 1: Write the migration** — transcribe the seven tables from `drizzle/0000_orange_zzzax.sql` with the spec §6 re-cut applied mechanically:
  - every `"created_at" text` / `"updated_at" text` / `"deleted_at" text` / `"deadline_at" text` / `start_at`/`end_at` → `timestamptz`
  - `channels jsonb` stays `jsonb`
  - keep every column name, default, PK, FK (`ON DELETE` behaviors included) and index from the drizzle file — FKs now point at the Limen-generated `users`/`organizations` table names from `migrations/00002_auth.sql` (open it and use the actual names/ID types)
  - skip: `push_subscriptions` (dropped), auth/billing tables (gone or Limen's)
- [ ] **Step 2: `sqlc.yaml`** —

```yaml
version: "2"
sql:
  - engine: postgresql
    schema: migrations
    queries: internal/polls/queries
    gen:
      go:
        package: queries
        out: internal/polls/queries
        sql_package: database/sql
        emit_interface: true
```

- [ ] **Step 3: Seed `polls.sql` with the first queries** (`GetPoll`, `InsertPoll`, `ListOptionsByPoll`, …) as Task 2 needs them; run `sqlc generate`; commit generated code.
- [ ] **Step 4: Verify** — `go test ./internal/db/` green with the new tables asserted.
- [ ] **Step 5: Commit** — `git commit -m "feat(polls): domain schema (timestamptz/jsonb re-cut) and sqlc scaffolding"`

---

### Task 2: Poll service port

**Files:**
- Create: `internal/polls/service.go`, more queries in `internal/polls/queries/*.sql`
- Test: `internal/polls/service_test.go`

**Source to port:** `src/server/polls/service.ts` (all exported functions) + `src/server/polls/schemas.ts` (validation rules → `Validate()` methods) + `src/server/polls/viewmodel.ts` (the response shapes).

**Interfaces:**
- Produces (signatures later tasks and plan 8's API client rely on):

```go
package polls

type Service struct { /* db *sql.DB, q *queries.Queries */ }
func NewService(sqlDB *sql.DB) *Service

// Types mirror viewmodel.ts field-for-field (JSON tags in camelCase to match the frontend).
type PollView struct { /* poll + options + participants + votes + comments + claim counts */ }
type PollSummary struct { /* id, type, title, status, counts, createdAt */ }

type CreatePollInput struct { /* port of schemas.ts createPollSchema; Validate() error */ }
func (s *Service) Create(ctx context.Context, orgID, userID string, in CreatePollInput) (*PollView, error)
func (s *Service) GetView(ctx context.Context, pollID string, viewer Viewer) (*PollView, error) // Viewer{UserID or GuestParticipantID}
func (s *Service) Update(ctx, pollID, orgID string, in UpdatePollInput) (*PollView, error)
func (s *Service) SetStatus(ctx, pollID, orgID, status string) error       // open | closed
func (s *Service) Finalize(ctx, pollID, orgID, optionID string) error      // + enqueues finalized mail via notifications (Task 4)
func (s *Service) Delete(ctx, pollID, orgID string) error                  // soft delete (deleted_at)
func (s *Service) Duplicate(ctx, pollID, orgID, userID string) (*PollView, error)
func (s *Service) ListMine(ctx, orgID string) ([]PollSummary, error)
func (s *Service) CloseExpired(ctx, pollID string) (bool, error)           // job handler body
```

- Org authorization rule (port from `requireManagedPoll` + the org-authz tests): every managing call verifies the poll belongs to `orgID`; wrong org → `ErrForbidden`, missing/deleted → `ErrNotFound` (sentinel errors in `internal/polls/errors.go`, mapped to envelope codes by handlers).
- Mutations that change what a viewer sees (`Update`, `SetStatus`, `Finalize`) emit `rooms.Emit(tx, "poll:"+pollID, "poll.updated", <fresh view fragment>)` in-tx — match the event names in `src/do/protocol.ts` (read it; the frontend's ws client in plan 8 keys on them).

- [ ] **Step 1: Failing tests** — table-driven against `testdb` (helper `seedOrgAndUser(t, d)` inserts a user+org directly via SQL). Cover per ported function: happy path, wrong-org → forbidden, deleted → not-found, `Finalize` sets `finalized_option_id` + status, `Duplicate` copies options but not participants/votes, `CloseExpired` only closes past-deadline open polls, validation table for `CreatePollInput.Validate()` ported from `src/server/polls/__tests__/schemas.test.ts` case-for-case.
- [ ] **Step 2: Run to verify failure. Step 3: implement (adding sqlc queries as needed + `sqlc generate`). Step 4: green.**
- [ ] **Step 5: Commit** — `git commit -m "feat(polls): poll service port with org authorization"`

---

### Task 3: Participants, votes, comments, claims — with the overbooking proof

**Files:**
- Create: `internal/polls/participants.go`, `internal/polls/claims.go`
- Test: `internal/polls/participants_test.go`, `internal/polls/claims_test.go`

**Source to port:** `src/server/polls/participants.ts`, `claims.ts` (its `applyClaim` doc comments explain the capacity rules — carry them), `claim-auth.ts`, `comment-auth.ts`.

**Interfaces:**
- Produces:

```go
// AddParticipant creates participant + votes; anonymous callers get back a guest token (auth.MintGuestToken).
func (s *Service) AddParticipant(ctx, pollID string, in ParticipantInput, viewer Viewer) (*ParticipantResult, error)
func (s *Service) UpdateParticipant(ctx, pollID, participantID string, in ParticipantInput, viewer Viewer) error
func (s *Service) RemoveParticipant(ctx, pollID, participantID string, viewer Viewer) error
func (s *Service) AddComment(ctx, pollID string, in CommentInput, viewer Viewer) (*Comment, error)
func (s *Service) DeleteComment(ctx, pollID, commentID string, viewer Viewer) error
// Claim/Unclaim implement sign-up sheets. THE atomicity contract:
// the option row is locked with SELECT ... FOR UPDATE before the capacity check, in one tx.
func (s *Service) Claim(ctx, pollID, optionID string, in ClaimInput, viewer Viewer) (*ClaimResult, error) // ErrCapacityFull when full
func (s *Service) Unclaim(ctx, pollID, optionID string, viewer Viewer) error
```

- Authorization matrix ported from the TS: organiser (org member) may edit/remove anyone; a guest may edit/remove only the participant their token names; comment deletion — author or organiser.
- Every mutation emits the matching room event in-tx (`participant.added`, `vote.updated`, `comment.added`, `claim.added`, … — names from `src/do/protocol.ts`).

- [ ] **Step 1: Failing tests** — port the behavioral cases from `claims.workers.test.ts`, `participants.workers.test.ts`, `claim-auth.workers.test.ts` (read them; they encode the real rules: max claims per participant, email-required polls, capacity boundaries, token scoping).
- [ ] **Step 2: The concurrency test** (spec §9 — this exact test):

```go
func TestClaimLastSlotExactlyOneWinner(t *testing.T) {
    d := testdb.New(t)
    // seed sheet with one option, capacity 1
    const racers = 16
    var wins atomic.Int32
    var wg sync.WaitGroup
    start := make(chan struct{})
    for i := 0; i < racers; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            <-start
            _, err := svc.Claim(ctx, pollID, optionID, claimInputFor(i), guestViewer(i))
            if err == nil { wins.Add(1) } else if !errors.Is(err, polls.ErrCapacityFull) { t.Error(err) }
        }(i)
    }
    close(start); wg.Wait()
    if wins.Load() != 1 { t.Fatalf("winners = %d, want exactly 1", wins.Load()) }
    // and the table agrees:
    // SELECT count(*) FROM votes/claims for that option == 1
}
```

- [ ] **Step 3: Implement** — `Claim` opens a tx, `SELECT capacity FROM poll_options WHERE id = $1 FOR UPDATE`, counts existing claims, inserts or returns `ErrCapacityFull`, emits, commits. **Step 4: run to green, including `-race`:** `go test ./internal/polls/ -race -run Claim -count 5`.
- [ ] **Step 5: Commit** — `git commit -m "feat(polls): participants/votes/comments/claims with FOR UPDATE capacity proof"`

---

### Task 4: Notifications + poll mail + timers

**Files:**
- Create: `internal/polls/notifications.go`, `internal/polls/timers.go`
- Test: `internal/polls/notifications_test.go`

**Source to port:** `src/server/notifications/{emit,recipients,subscriptions,claim-emails,finalize-emails}.ts` and the timer arming in `src/do/PollRoom.ts` (deadline close + digest — read its alarm logic; plan 2's job table is its replacement).

**Interfaces:**
- Produces:

```go
// Recipients resolves who gets mail for a poll event: organiser + followers with the channel on,
// minus the actor (port recipients.ts rules).
func (s *Service) UpdateNotificationPrefs(ctx, userID string, channels map[string]bool) error
func (s *Service) SetFollowing(ctx, pollID, userID string, following bool) error
// RegisterJobs wires poll job kinds into the worker:
//   "poll.deadline" (roomKey poll:<id>) → CloseExpired + emit + "closed" mail to recipients
//   "poll.digest"   (roomKey poll:<id>) → digest mail (port Digest.tsx data shape)
func (s *Service) RegisterJobs(w *jobs.Worker)
// Finalize (Task 2) and Claim (Task 3) call into:
//   enqueue finalized mail w/ .ics to every participant with an email (finalize-emails.ts)
//   enqueue claim_confirmation to the claimer (claim-emails.ts)
// Mail payloads follow the ids-only rule: {"pollId": ...} — the mail:poll handlers re-read and render.
```

- Deadline/digest arming: `Create`/`Update` schedule (`jobs.Schedule` upsert with roomKey) when `deadline_at` set; clearing the deadline cancels (`jobs.Cancel`) — port the re-arm semantics from PollRoom.
- [ ] **Step 1: Failing tests** — finalize enqueues N mails for N emailed participants (assert via `scheduled_jobs` rows, kind + payload ids only — no addresses in payloads); deadline job closes the poll and dead-letters nothing; prefs/following toggles change the recipient set (port `recipients.workers.test.ts` cases).
- [ ] **Step 2: Implement. Step 3: green. Step 4: commit** — `git commit -m "feat(polls): notifications, finalize/claim mail, deadline+digest timers"`

---

### Task 5: `.ics` writer

**Files:**
- Create: `internal/polls/ics.go`
- Test: `internal/polls/ics_test.go`

**Source to port:** `src/server/polls/ics.ts` (+ its tests).

**Interfaces:**
- Produces: `func BuildPollICS(ctx context.Context, q *queries.Queries, pollID string) (filename string, ics []byte, err error)` — VCALENDAR/VEVENT for the finalized option; all-day vs timed handling per `icsStartFromOption`; CRLF line endings; `PRODID://whenweall//EN`; escapes `,;\n` in text fields.

- [ ] **Step 1: Failing tests** ported from `ics.workers.test.ts` (timed event UTC formatting `YYYYMMDDTHHMMSSZ`, all-day `VALUE=DATE`, escaping, nil when not finalized).
- [ ] **Step 2: Implement (pure Go string building — no library). Step 3: green. Step 4: commit** — `git commit -m "feat(polls): ics writer port"`

---

### Task 6: Turnstile verification

**Files:**
- Create: `internal/httpserver/turnstile.go`
- Test: `internal/httpserver/turnstile_test.go`

**Source to port:** `src/server/http/turnstile.ts`.

**Interfaces:**
- Produces: `func VerifyTurnstile(ctx context.Context, secretKey, token, remoteIP string) error` (POST to `https://challenges.cloudflare.com/turnstile/v0/siteverify`, injectable base URL for tests) and `RequireCaptcha(cfg)` middleware: when capability on, reads `X-Captcha-Token`, 403 `captcha_failed` on failure; capability off → pass-through.

- [ ] **Step 1: Failing tests** with `httptest.Server` stub for siteverify (success / failure / timeout→fail-closed on public endpoints).
- [ ] **Step 2: Implement. Step 3: green. Step 4: commit** — `git commit -m "feat(http): turnstile verification middleware"`

---

### Task 7: HTTP handlers — the poll API surface

**Files:**
- Create: `internal/polls/handlers.go`, `internal/httpserver/respond.go` (shared JSON envelope helpers `respond.JSON/Err`)
- Test: `internal/polls/handlers_test.go`

**Interfaces:**
- Consumes: `auth.Service` (RequireSession/FromContext/VerifyGuestToken via an interface `polls.Auth` to keep the seam), Turnstile middleware, rate limiting.
- Produces — `func (s *Service) Register(mux *http.ServeMux, a Auth, cfg *config.Config)` mounting (this table is the frontend contract for plan 8):

```
POST   /api/v1/polls                      auth        → Create        (201, PollView)
GET    /api/v1/polls/{id}                 public      → GetView       (guest token via ?token= or X-Guest-Token)
PATCH  /api/v1/polls/{id}                 auth+org    → Update
POST   /api/v1/polls/{id}/status          auth+org    → SetStatus
POST   /api/v1/polls/{id}/finalize        auth+org    → Finalize
DELETE /api/v1/polls/{id}                 auth+org    → Delete
POST   /api/v1/polls/{id}/duplicate       auth+org    → Duplicate
GET    /api/v1/polls                      auth        → ListMine (active org)
POST   /api/v1/polls/{id}/participants    public+captcha → AddParticipant (returns guestToken for anonymous)
PATCH  /api/v1/polls/{id}/participants/{pid}  public(token)|auth → UpdateParticipant
DELETE /api/v1/polls/{id}/participants/{pid}  public(token)|auth → RemoveParticipant
POST   /api/v1/polls/{id}/comments        public+captcha → AddComment
DELETE /api/v1/polls/{id}/comments/{cid}  public(token)|auth → DeleteComment
POST   /api/v1/polls/{id}/claims          public+captcha → Claim (409 capacity_full)
DELETE /api/v1/polls/{id}/claims/{oid}    public(token)|auth → Unclaim
GET    /api/v1/polls/{id}/calendar.ics    public      → BuildPollICS (Content-Type text/calendar)
GET    /api/v1/polls/{id}/roster.csv      auth+org    → buildRosterCsv port (text/csv)
POST   /api/v1/me/notification-prefs      auth        → UpdateNotificationPrefs
POST   /api/v1/polls/{id}/following       auth        → SetFollowing
GET    /api/v1/config                     public      → capability flags (port config.functions.ts: {turnstileSiteKey?, googleEnabled, oidcEnabled, oidcName})
```

- [ ] **Step 1: Failing handler tests** — `httptest` through the full middleware chain: status codes, envelope codes (`invalid` carries `fields`), guest-token paths, captcha rejection when capability on, 404 JSON for unknown ids. One test per row above minimum.
- [ ] **Step 2: Implement (thin: decode → Validate → service → respond). Step 3: green. Step 4: wire `Register` into httpserver.New + serve(); `go test ./...` green. Step 5: commit** — `git commit -m "feat(polls): http surface for polls and sign-up sheets"`
