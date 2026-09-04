# Go Rewrite Plan 3/8 — Auth & Organizations

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Limen wired behind our `internal/auth` seam: email+password with verification, capability-flagged Google OAuth / generic OIDC / magic links / TOTP 2FA, Limen's organization plugin with invitation mail through our queue, personal-org-on-signup, staff role, guest tokens, and rate-limited auth endpoints.

**Architecture:** Limen (`github.com/thecodearcher/limen`) mounts at `/api/v1/auth` via its own handler. **No package outside `internal/auth` imports Limen** — handlers depend on `auth.FromContext`, `RequireSession`, `RequireStaff` (the seam; Limen is young and must be swappable). Its email callbacks feed `mailer.Enqueue`. Its tables come from its CLI migration generator, folded into goose migration `00002`.

**Tech Stack:** `github.com/thecodearcher/limen` + plugins `credential-password`, `oauth` (+`oauth-google`, `oauth-generic`), `magic-link`, `two-factor`, `organization`; adapter `adapters/sql`.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§3, incl. the org-plugin amendment)

## Global Constraints

Plan 1's Global Constraints apply. Additionally:
- Pin Limen and every plugin to one reviewed commit (`go get github.com/thecodearcher/limen@<sha>` — record the sha in go.mod, obviously — and the same version for each plugin module).
- `Config.Secret` = `cfg.LimenSecret` (sha256 of AUTH_SECRET, exactly 32 bytes — Limen rejects anything else).
- Auth API paths the frontend (plan 8) codes against are whatever Limen mounts under `/api/v1/auth` — verified and recorded in Task 2, e.g. `POST /api/v1/auth/signup/credential`, `POST /api/v1/auth/signin/credential`, `POST /api/v1/auth/passwords/request-reset`, `POST /api/v1/auth/passwords/reset`, `POST /api/v1/auth/verify-email`, `GET /api/v1/auth/me`, `POST /api/v1/auth/signout`, `GET /api/v1/auth/{provider}/authorize|callback`, magic-link `POST .../signin` + `GET .../verify`, org routes under the org plugin's prefix (`POST /`, `GET /`, `POST /invitations`, `POST /invitations/respond`, `GET /invitations/token/{token}`, `POST /switch`, …).

---

### Task 1: Limen tables → goose migration 00002

**Files:**
- Create: `migrations/00002_auth.sql`, `internal/auth/schemagen/main.go` (a tiny dev-only program), `docs/limen-migrations.md` (how to regenerate)

**Interfaces:**
- Produces: all Limen core + plugin tables, plus our `staff_users`.

- [ ] **Step 1: Generate Limen's DDL**

Write `internal/auth/schemagen/main.go`: constructs the *exact* `limen.New` config Task 2 will use (all plugins on, `CLI: &limen.CLIConfig{Enabled: true}`) pointed at a throwaway Postgres from `docker compose up -d db`; running it writes `.limen/schemas.json`. Then:

```bash
go run github.com/thecodearcher/limen/cmd/limen@<pinned-sha> generate migrations \
  --driver postgres --dsn "postgres://whenweall:whenweall@localhost:5433/whenweall" --output /tmp/limen-migrations
```

- [ ] **Step 2: Fold into `migrations/00002_auth.sql`**

Concatenate the generated `CREATE TABLE`s into one goose migration (Up = the DDL, Down = drops in reverse). Append our own table:

```sql
-- Platform staff. Ours, not Limen's: extending Limen's user schema would couple the
-- admin console to the auth library — a flag table does not.
CREATE TABLE staff_users (
  user_id text PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT now()
);
```

(If Limen generates non-text user PKs, keep whatever it generates and make `staff_users.user_id` match that type — the seam normalizes IDs to `string` in Go regardless.)

Document the regeneration steps in `docs/limen-migrations.md` (schemagen → CLI → hand-fold; future Limen upgrades produce ALTER migrations the same way, as new goose files).

- [ ] **Step 3: Verify** — `go test ./internal/db/` still green (migrations apply on the template), plus extend `TestMigrationsCreateInfraTables`'s table list with `users` (or Limen's user table name as generated), `staff_users`.

- [ ] **Step 4: Commit** — `git commit -m "feat(auth): limen schema baseline + staff_users"`

---

### Task 2: internal/auth — the seam and the Limen wiring

**Files:**
- Create: `internal/auth/auth.go` (service + Limen config), `internal/auth/session.go` (middleware + context), `internal/auth/routes.txt` (the verified route list — the frontend contract)
- Test: `internal/auth/auth_test.go`

**Interfaces:**
- Consumes: `config.Config`, `*sql.DB`, `mailer.Enqueue` (via a small `Enqueuer` func field so tests can capture mail).
- Produces (this is THE seam — later plans import only these):

```go
package auth

type Service struct { /* limen *limen.Limen, orgs organization.API, db, cfg, enqueueMail func(ctx, db.DBTX, mailer.Message) error */ }

func New(cfg *config.Config, sqlDB *sql.DB) (*Service, error)
func (s *Service) Handler() http.Handler // mount at /api/v1/auth/ (strip prefix handled inside via WithHTTPBasePath)

type Session struct {
    UserID      string
    Email       string
    ActiveOrgID string // "" when none
    Staff       bool
}

// Middleware resolves the Limen session cookie (if any) and stores *Session in ctx.
// It never rejects — RequireSession does that.
func (s *Service) Middleware(next http.Handler) http.Handler
func FromContext(ctx context.Context) (*Session, bool)
func (s *Service) RequireSession(next http.HandlerFunc) http.HandlerFunc // 401 envelope {"error":{"code":"unauthenticated",...}}
func (s *Service) RequireStaff(next http.HandlerFunc) http.HandlerFunc   // 401/403 {"code":"forbidden"}
// RequireOrgMember returns the caller's Session after verifying membership of orgID (403 otherwise).
func (s *Service) RequireOrgMember(ctx context.Context, orgID string) (*Session, error)
func (s *Service) MakeStaff(ctx context.Context, email string) error // used by create-staff-user
```

- Limen construction inside `New` (real API, verified against the pinned source):

```go
plugins := []limen.Plugin{
    credentialpassword.New(
        credentialpassword.WithSendPasswordResetEmail(func(email, token string) {
            s.enqueueAuthMail(email, "reset_password", token)
        }),
    ),
    organization.New(
        organization.WithSendInvitationMail(func(ctx context.Context, d *organization.SendInvitationMailData) {
            // template org_invite; link {AppURL}/accept-invitation/{d.Invitation token}
        }),
    ),
    magiclink.New(magiclink.WithSendMagicLink(func(m magiclink.MagicLinkMessage) { /* template magic_link */ })),
    twofactor.New(),
}
if cfg.Capabilities.Google || cfg.Capabilities.OIDC {
    var providers []oauth.Provider
    if cfg.Capabilities.Google {
        providers = append(providers, oauthgoogle.New(
            oauthgoogle.WithClientID(cfg.GoogleClientID),
            oauthgoogle.WithClientSecret(cfg.GoogleClientSecret),
        ))
    }
    if cfg.Capabilities.OIDC {
        providers = append(providers, oauthgeneric.New(
            oauthgeneric.WithName(cfg.OIDCName),
            oauthgeneric.WithDiscoveryURL(cfg.OIDCIssuer),
            oauthgeneric.WithClientID(cfg.OIDCClientID),
            oauthgeneric.WithClientSecret(cfg.OIDCClientSecret),
        ))
    }
    plugins = append(plugins, oauth.New(oauth.WithProviders(providers...)))
}

a, err := limen.New(&limen.Config{
    BaseURL:  cfg.AppURL,
    Database: sqladapter.NewPostgreSQL(sqlDB),
    Secret:   cfg.LimenSecret,
    Plugins:  plugins,
    Email: limen.NewDefaultEmailConfig(limen.WithEmailVerification(
        limen.WithSendEmailVerificationMail(func(email, token string) { /* template verify_email */ }),
    )),
    HTTP: limen.NewDefaultHTTPConfig(
        limen.WithHTTPBasePath("/api/v1/auth"),
        limen.WithHTTPHooks(s.personalOrgHooks()), // Task 3
    ),
})
```

  (Exact option names must be re-checked against the pinned sha while implementing; the shapes above were read from source at planning time. If an option is missing at that sha, adapt at the call site — the seam means nothing else changes.)
- `Middleware` calls `s.limen.GetSession(r)`; on success builds `Session{UserID: fmt.Sprint(v.User.ID), Email: v.User.Email, ActiveOrgID: from orgs.GetActiveOrganizationID, Staff: EXISTS staff_users}` and stashes it via a private ctx key.
- Auth mails: `enqueueAuthMail` builds `mailer.Message{To: email, Template: name, Data: {"URL": cfg.AppURL + path + "?token=" + token}}` and enqueues **outside** any tx (callbacks fire post-commit inside Limen handlers; use `s.db` directly). Callbacks must never block or panic — log enqueue errors.

- [ ] **Step 1: Write the failing tests**

Integration tests over `httptest.NewServer` wrapping `Middleware(mux)` with the auth handler mounted (helper `newTestService(t)` returns Service + captured mail slice + base URL; cookie jar client):

```go
func TestSignupSigninMeFlow(t *testing.T) {
    // POST /api/v1/auth/signup/credential {"email","password"} → 2xx
    // exactly one captured mail with Template "verify_email" and a token URL
    // POST /api/v1/auth/signin/credential → 2xx, sets session cookie
    // GET /api/v1/auth/me → 200, email matches
    // FromContext-based probe route "/probe" returns the Session — UserID non-empty, Staff false
}
func TestRequireSessionRejectsAnonymous(t *testing.T)      // 401 with code "unauthenticated"
func TestPasswordResetEnqueuesMail(t *testing.T)           // request-reset → captured "reset_password" mail; reset with token → can sign in with new password
func TestStaffFlagAndRequireStaff(t *testing.T)            // signed-in non-staff → 403 on staff probe; MakeStaff(email) → 200
func TestOAuthRoutesAbsentWithoutConfig(t *testing.T)      // GET /api/v1/auth/google/authorize → 404/4xx when capability off
```

- [ ] **Step 2: Run to verify failure.** — `go test ./internal/auth/` → FAIL.

- [ ] **Step 3: Implement** per Interfaces. While here, run the test server once with a route-dump (iterate a request over the known list) and write the confirmed method+path table to `internal/auth/routes.txt` — plan 8 codes the frontend against this file.

- [ ] **Step 4: Run to green.** `go test ./internal/auth/ -v`

- [ ] **Step 5: Mount in httpserver** — `New` gains the service: `mux.Handle("/api/v1/auth/", authSvc.Handler())`, and `Server` wraps the whole mux in `authSvc.Middleware`. Update `cmd/whenweall` serve wiring. `go test ./...` stays green.

- [ ] **Step 6: Commit** — `git commit -m "feat(auth): limen behind the internal/auth seam, mail via the queue"`

---

### Task 3: Personal org on signup

Port the *intent* of `src/server/auth/personal-org.ts` (read it first): every user always has at least one organization; the personal one is created automatically and named after the user's email local part.

**Files:**
- Modify: `internal/auth/auth.go` (`personalOrgHooks`)
- Test: `internal/auth/personal_org_test.go`

**Interfaces:**
- Consumes: `organization.Use(s.limen)` API (`CreateOrganization`), Limen `Hooks` (`limen.WithHTTPHooks`).
- Produces: after-hook matched to route IDs `signup` and `oauth-callback`: reads `ctx.GetAuthResult()`; when a brand-new user has zero orgs, calls `orgs.CreateOrganization(ctx, user, &organization.CreateOrganizationRequest{Name: localPart, Slug: ""})` (plugin generates slug). Idempotent: checks `ListOrganizations` count first, so an OAuth sign-in of an existing user does nothing.

- [ ] **Step 1: Failing test** — extend the signup flow test: after signup, `orgs.ListOrganizations` for that user returns exactly 1 org; sign out/in again → still 1.
- [ ] **Step 2: Implement the hook. Step 3: green. Step 4: commit** — `git commit -m "feat(auth): personal organization on first signup"`

---

### Task 4: Guest participant tokens

Port `src/server/polls/claim-auth.ts` + `comment-auth.ts` token logic (read both; the shape is: participant edit tokens handed to anonymous voters, HMAC over the id, constant-time verify). Plans 4/5 consume this.

**Files:**
- Create: `internal/auth/guest.go`
- Test: `internal/auth/guest_test.go`

**Interfaces:**
- Produces:

```go
// MintGuestToken returns "<participantID>.<hex hmac-sha256(secret, participantID)>".
func (s *Service) MintGuestToken(participantID string) string
// VerifyGuestToken returns the participantID iff the signature verifies (hmac.Equal).
func (s *Service) VerifyGuestToken(token string) (participantID string, ok bool)
```

- [ ] **Step 1: Failing tests** — round-trip; tampered id fails; tampered sig fails; empty fails; token from a different secret fails.
- [ ] **Step 2: Implement (HMAC-SHA256 keyed on `cfg.AuthSecret`). Step 3: green. Step 4: commit** — `git commit -m "feat(auth): hmac guest participant tokens"`

---

### Task 5: Rate limiting on auth endpoints

Port `src/server/http/rate-limit-store.ts` + `rate-limit.middleware.ts` (read both — fixed window over the UNLOGGED `rate_limits` table, fail-open on DB error) and wrap the hot auth routes.

**Files:**
- Create: `internal/httpserver/ratelimit.go`
- Test: `internal/httpserver/ratelimit_test.go`

**Interfaces:**
- Produces:

```go
// RateLimit allows `limit` hits per `window` per key; over-limit → 429 {"error":{"code":"rate_limited",...}} with Retry-After.
// keyFn returns "" to skip limiting for that request. Errors reading the store fail open (log WARN).
func RateLimit(sqlDB *sql.DB, name string, limit int, window time.Duration, keyFn func(*http.Request) string) func(http.Handler) http.Handler
// ClientIP honors TRUST_PROXY: X-Forwarded-For first value when trusted, else RemoteAddr host.
func ClientIP(r *http.Request, trustProxy bool) string
```

- SQL (one statement, matches the store): `INSERT INTO rate_limits (key, count, reset_at) VALUES ($1, 1, now() + $2) ON CONFLICT (key) DO UPDATE SET count = CASE WHEN rate_limits.reset_at < now() THEN 1 ELSE rate_limits.count + 1 END, reset_at = CASE WHEN rate_limits.reset_at < now() THEN excluded.reset_at ELSE rate_limits.reset_at END RETURNING count, reset_at`.
- Wrap in httpserver: `POST /api/v1/auth/signin/credential`, `signup/credential`, `passwords/request-reset`, magic-link signin — 10/min per `name+":"+ClientIP` (mirror the numbers in `src/server/auth/rate-limit-storage.ts` if they differ — read it).

- [ ] **Step 1: Failing tests** — 10 pass, 11th → 429 with Retry-After; window expiry (`UPDATE rate_limits SET reset_at = now() - interval '1s'`) resets; closed DB → requests pass (fail-open); ClientIP table (proxy on/off).
- [ ] **Step 2: Implement. Step 3: green. Step 4: commit** — `git commit -m "feat(http): fixed-window rate limiting on auth endpoints"`

---

### Task 6: create-staff-user subcommand

Replaces the seed-based staff bootstrap (`docs/` admin runbook updates in plan 8).

**Files:**
- Modify: `cmd/whenweall/main.go`
- Test: `internal/auth/staff_cli_test.go` (test `Service.MakeStaff` path; the cmd shim is thin)

- [ ] **Step 1: Failing test** — `MakeStaff` on an existing email inserts into `staff_users` (idempotent on repeat); unknown email → error mentioning "no user".
- [ ] **Step 2: Implement** — `whenweall create-staff-user --email x@y.z`: load config, open db, `auth.New`, `MakeStaff`, print confirmation. Exit 1 with the error on stderr otherwise.
- [ ] **Step 3: green; manual check** via compose:

```bash
docker compose exec app /whenweall create-staff-user --email you@example.com
```

- [ ] **Step 4: Commit** — `git commit -m "feat(cmd): create-staff-user bootstrap subcommand"`

---

### Task 7: CSRF / Origin check for mutating API routes

Spec §2: `SameSite=Lax` cookie (Limen's default) + an Origin check of our own for `/api/v1/*` mutations outside Limen's mount (Limen checks its own).

**Files:**
- Create: `internal/httpserver/origin.go`
- Test: `internal/httpserver/origin_test.go`

**Interfaces:**
- Produces: `CheckOrigin(appURL string) func(http.Handler) http.Handler` — for POST/PUT/PATCH/DELETE, when an `Origin` header is present it must equal the AppURL origin, else 403 `{"error":{"code":"bad_origin",...}}`. GET/HEAD/OPTIONS and header-less requests (curl, same-origin fetches in older agents) pass.

- [ ] **Step 1: Failing tests** — cross-origin POST 403; same-origin POST passes; GET cross-origin passes; no-header POST passes.
- [ ] **Step 2: Implement; wire into the API route chain in `server.go`. Step 3: green. Step 4: commit** — `git commit -m "feat(http): origin check on mutating api routes"`
