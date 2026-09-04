# Completion plans 2026-09-03 — index and shared contract

Six plans, executed in order; each leaves the tree green:

1. [01 auth, account, locale foundation](2026-09-03-complete-01-auth-account.md)
2. [02 HTTP hardening, infra, docs drift](2026-09-03-complete-02-http-infra-docs.md)
3. [03 polls and sign-up sheets](2026-09-03-complete-03-polls.md)
4. [04 bookings (Google Calendar disabled)](2026-09-03-complete-04-bookings.md)
5. [05 admin console UI and jobs](2026-09-03-complete-05-admin-jobs.md)
6. [06 Playwright coverage, harness, CI image job](2026-09-03-complete-06-e2e-ci.md)

They close the gaps found by the 2026-09-03 parity audit of this branch against the old TypeScript
backend on `main`. The contract below is what the plans were written against.

---

# Shared contract for the 2026-09-03 completion plans (plans A–F)

Repo: /home/anders/projects/refsdal/whenweall, branch feat/go-rewrite (PR #67). Go 1.27 toolchain on PATH
(`go`), golangci-lint/sqlc/goose in ~/go/bin. Old TS code is only in git history on `main`
(`git show main:<path>`). Spec: docs/superpowers/specs/2026-09-01-go-rewrite-design.md. Existing plans:
docs/superpowers/plans/2026-09-01-go-rewrite-0*.md (read the relevant one for conventions).

## User decisions (fixed — do not re-litigate)
1. Email verification: RESTORE the gate. Unverified accounts cannot use the app; verify link must work;
   resend flow restored.
2. Google Calendar sync: DISABLE for now. Hide the UI behind the capability flag, status always "not
   connected", README says not yet available. Do NOT build a custom consent flow. Keep the Go sync code.
3. Email locale: RESTORE. Per-user locale persisted; guest forms send locale; nb mail renders again;
   dates in mail are locale-aware.
4. Everything lands as commits on feat/go-rewrite. A new CI job runs Playwright against the built
   Docker image with the compose hardening flags.
Still dropped by design (never reintroduce): passkeys, billing, magic links, TOTP 2FA, staff impersonation,
SSR/OG tags, web push, booking-page follower notifications (verifier ruled intentional).

## Execution order
A (auth/account/locale foundation) → B (http/security/infra/docs) → C (polls) → D (bookings) →
E (admin/jobs) → F (e2e + CI). A later plan may consume anything an earlier plan "Produces".

## Migration numbering (goose, migrations/NNNNN_name.sql)
- 00009_user_profile.sql — Plan A (user_preferences table)
- 00010_drop_two_factor.sql — Plan B
- 00011_votes_answer_check.sql — Plan C (CHECK on votes.answer)
No other plan adds migrations. After any migration, run `sqlc generate` (sqlc.yaml at root) and commit
the regenerated internal/*/queries/*.go.

## Interfaces Plan A PRODUCES (everyone else consumes exactly these names)
Go, package internal/auth (file internal/auth/profile.go unless stated):
- `type Profile struct { UserID string; Name string; Locale string; EmailVerified bool }`
- `func (s *Service) GetProfile(ctx context.Context, userID string) (Profile, error)` — Name falls back to
  nameFromEmail(email) when first_name/last_name blank; Locale falls back to "en"; never returns a zero
  Locale.
- `func (s *Service) SetProfile(ctx context.Context, userID string, name *string, locale *string) error`
  — nil = unchanged; name trimmed, 1..80 chars; locale must be one of `mailer.SupportedLocales`.
- `func (s *Service) LocaleFor(ctx context.Context, userID string) string` — cheap helper, "en" fallback.
- `func (s *Service) DeleteOwnAccount(ctx context.Context, userID string) error` — cascades exactly like
  admin.DeleteUser (reuse its code by moving the cascade into a shared function
  `internal/auth.CascadeDeleteUser(ctx, tx, userID)`; Plan A moves it, internal/admin calls it).
- Session struct (internal/auth/session.go) gains `EmailVerified bool`. The auth middleware
  `RequireSession`/`WithOrgSession` (internal/httpserver) return 403 `{"error":{"code":"email_unverified"}}`
  for a session whose user is unverified, EXCEPT for: GET /api/v1/auth/me, POST /api/v1/auth/signout,
  POST /api/v1/auth/verify-email, POST /api/v1/auth/email-verifications, GET /api/v1/config.
  OAuth-created users count as verified (Limen sets email_verified_at on OAuth; plan A verifies this in
  Limen source and, if not, marks them verified in the after-callback hook).
- Table `user_preferences(user_id text primary key references users(id) on delete cascade, locale text not
  null default 'en', updated_at timestamptz not null default now())`.
HTTP (mounted in internal/auth or internal/httpserver, under /api/v1):
- `GET /api/v1/auth/me` response gains `emailVerified: boolean`, `locale: string`, and `name` is the
  stored display name (fallback nameFromEmail).
- `PATCH /api/v1/me` body `{ "name"?: string, "locale"?: string }` → 204. Session required (verified or not —
  unverified users may set locale).
- `DELETE /api/v1/me` body `{ "password"?: string }` → 204; credential accounts must supply the current
  password (verified via Limen's exported password hasher — plan A locates it), OAuth-only accounts omit it.
- Captcha on auth: middleware in internal/httpserver applied to POST /api/v1/auth/signin/credential,
  /signup/credential, /passwords/request-reset when cfg.Capabilities.Turnstile: reads `X-Captcha-Token`,
  calls the existing VerifyTurnstile, 403 `captcha_failed` on failure.
Go, package internal/mailer (file internal/mailer/format.go):
- `var SupportedLocales = []string{"en", "nb"}`
- `func FormatDateTime(locale string, t time.Time, loc *time.Location) string` — en: "Mon 1 Sep, 18:30";
  nb: "man. 1. sep., 18:30" (24h clock for both; nb weekday/month names from a small table).
- `func FormatDate(locale string, t time.Time, loc *time.Location) string` — en "Tue 1 Sep", nb "tir. 1. sep."
- `func FormatTimeRange(locale string, start, end time.Time, loc *time.Location) string` — "18:30–19:30".
Web (web/src):
- `web/src/lib/captcha.ts`: `export function useCaptchaEnabled(): boolean` (true iff publicConfig
  .turnstileSiteKey is non-empty). `TurnstileField` renders null when disabled. Every submit gate becomes
  `if (captchaEnabled && !captchaToken)`. The api client only sends X-Captcha-Token when token non-null.
- `web/src/api/auth.ts`: `AuthUser` gains `emailVerified: boolean` and `locale: Locale`;
  `updateProfile(patch: {name?: string; locale?: string}): Promise<void>`; `deleteOwnAccount(password?:
  string): Promise<void>`; `signInWithCredential/signUpWithCredential/requestPasswordReset` accept an
  optional trailing `captchaToken?: string | null` and forward it; `signUpWithCredential(email, password,
  name, captchaToken?)` persists the name (Plan A chooses the mechanism; contract is that GET /me returns it).
- `web/src/routes/verify-email.tsx` consumes `?token=`; `/login` after sign-in checks `user.emailVerified`
  and shows the unverified card with a resend button when false.
- Org switcher: `switchOrganization(orgId: string): Promise<void>` in web/src/api/auth.ts; accept-invitation
  calls it after accept; UserMenu lists orgs (`listOrganizations()`) with a switch action.
- Locale: `LocaleSwitcher` also calls `updateProfile({locale})` when signed in; guest forms (vote, claim,
  book) send `locale: getLocale()` — Plan A adds the client plumbing in web/src/api/polls.ts and
  bookings.ts (`locale` field on the request bodies; Go already accepts it).

## Interfaces Plan B PRODUCES
- `httpserver.SecurityHeaders` emits CSP (hashes of inline scripts computed at boot from the embedded
  index.html), Permissions-Policy, HSTS when APP_URL is https.
- `httpserver.PublicRateLimit` is skipped when cfg.EnableTestRoutes (like the auth limiter).
- `http.MaxBytesHandler(1<<20)` wraps /api/.
- compose.yaml `app` has `image: ghcr.io/refsdal/whenweall:latest` plus `build:`.
- Go line moved to 1.26 (go.mod `go 1.26`, Dockerfile golang:1.26-alpine, CI setup-go 1.26, README).
- internal/testdb fails (t.Fatalf) instead of skipping when `CI` env var is set.

## Conventions
- TDD: failing test first, run it, implement, run, commit. Go tests use internal/testdb (live Postgres);
  handler tests use the existing httptest helpers in each package. Web: vitest + Testing Library.
- Commit messages: conventional (`fix(auth): …`), ending with the two trailer lines:
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and
  `Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p`.
- Error envelope `{"error":{"code","message"}}`; codes snake_case; the SPA maps codes to paraglide keys.
- Every new user-facing string goes into web/messages/en.json AND nb.json.
- Gates before each plan is declared done: `go build ./... && go vet ./... && golangci-lint run ./... &&
  go test ./...`; `cd web && bun run typecheck && bun run lint && bunx vitest run`; `bunx playwright test`.
