# Completion Plan A — Auth, Account & Locale Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the auth/account features the Go rewrite dropped — a working, enforced email-verification gate, server-verified captcha on the three hot auth routes (and a UI that works when captcha is off), display names, per-user locale (persisted, used by mails, sent by guest forms), self-service account deletion, an OIDC button, an org switcher, and Limen hardening (rate-limit key, OIDC verified-email guard, org-slug rules) — with Go and vitest coverage for each.

**Architecture:** Everything Limen stays behind `internal/auth` (the seam). New per-user state lives in our own `user_preferences` table (never an extension of Limen's schema). The verification gate runs in two layers, exactly like the existing lock: `RequireSession`/`RequireStaff`/`WithOrgSession` for our handlers, and `AuthMountGuard` (the renamed `LockedSessionMiddleware`) for Limen's own mount. Account routes (`/api/v1/me*`) are thin `internal/httpserver` handlers over new `auth.Service` methods. The SPA gains one `useCaptchaEnabled()` hook that every captcha gate consults, and route guards that send unverified users to `/verify-email`.

**Tech Stack:** Go 1.27 toolchain (`go 1.25.7` in go.mod until Plan B), Limen `v0.2.2-0.20260813001613-c6a34aa6dcb4` + plugins `v0.2.1-…` (organization `v0.1.1-…`), pgx via `database/sql`, goose, sqlc, testcontainers (`internal/testdb`); web: Vite + React + TanStack Router, paraglide, vitest + Testing Library + msw.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§2 API/frontend, §3 auth & organizations, §5 mail localization, §8 capability flags) plus the shared contract for the 2026-09-03 completion plans, restated below.

## Global Constraints

- Branch `feat/go-rewrite`; every task ends in a commit on it. Conventional commit subjects (`fix(auth): …`, `feat(web): …`), body optional, and ALWAYS these two trailer lines:
  ```
  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
  ```
- Fixed user decisions (do not re-litigate): email verification GATES the app (verify link must work, resend restored); per-user email locale RESTORED (persisted, guest forms send it, nb mail renders, mail dates locale-aware); Google Calendar sync stays disabled (not this plan); dropped forever: passkeys, billing, magic links, TOTP 2FA, staff impersonation, SSR/OG, web push.
- The only migration this plan adds is `migrations/00009_user_profile.sql`. After it, run `sqlc generate` (sqlc.yaml at the repo root) and commit the regenerated `internal/polls/queries/*.go` and `internal/bookings/queries/*.go`.
- No package outside `internal/auth` imports Limen. `internal/httpserver` imports `internal/auth`, so `internal/auth` must NEVER import `internal/httpserver` (that is why Task 8 introduces `internal/clientip`).
- Error envelope everywhere outside Limen's own mount: `{"error":{"code","message","fields"?}}`, snake_case codes. New codes introduced here: `email_unverified` (403), `captcha_failed` (403), `password_required` (400), `invalid_password` (403).
- Interfaces this plan PRODUCES (later plans consume exactly these names — do not rename):
  - `internal/auth`: `type Profile struct { UserID, Name, Locale string; EmailVerified bool }`; `(*Service).GetProfile(ctx, userID string) (Profile, error)`; `(*Service).SetProfile(ctx, userID string, name, locale *string) error`; `(*Service).LocaleFor(ctx, userID string) string`; `(*Service).DeleteOwnAccount(ctx, userID string) error`; `CascadeDeleteUser(ctx, tx *sql.Tx, userID string) error`; `Session.EmailVerified bool`.
  - `internal/mailer/format.go`: `var SupportedLocales = []string{"en", "nb"}`; `FormatDateTime(locale string, t time.Time, loc *time.Location) string`; `FormatDate(locale string, t time.Time, loc *time.Location) string`; `FormatTimeRange(locale string, start, end time.Time, loc *time.Location) string`.
  - HTTP: `GET /api/v1/auth/me` user object gains `emailVerified`, `locale`, `name`, `hasPassword`; `PATCH /api/v1/me {name?, locale?}` → 204; `DELETE /api/v1/me {password?}` → 204; `GET /api/v1/me/organizations`; `POST /api/v1/me/active-organization {orgId}` → 204.
  - Web: `web/src/lib/captcha.ts` `useCaptchaEnabled(): boolean`; `web/src/api/auth.ts` `AuthUser.emailVerified`, `AuthUser.locale`, `updateProfile`, `deleteOwnAccount`, `listOrganizations`, `switchOrganization`, captcha-token trailing params on `signInWithCredential`/`signUpWithCredential`/`requestPasswordReset`.
- Table shape: `user_preferences(user_id bigint primary key references users(id) on delete cascade, locale text not null default 'en', updated_at timestamptz not null default now())`. (The contract wrote `user_id text`; Limen's `users.id` is `BIGSERIAL` — see `migrations/00002_auth.sql` — and a Postgres FK must match the referenced type, so `bigint` is the only valid spelling. The seam still exposes ids as Go strings.)
- Every new user-facing string goes into BOTH `web/messages/en.json` and `web/messages/nb.json` (`web/messages/__tests__/messages.test.ts` fails on key-set drift).
- TDD: failing test → run it → implement → run → commit. Go tests use `internal/testdb` (a live Postgres testcontainer; Docker must be running). Web tests: `cd web && bunx vitest run <file>`.
- Gates before declaring the plan done: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`; `cd web && bun run typecheck && bun run lint && bunx vitest run`.

## Limen facts confirmed against the pinned source (do not re-derive)

All paths relative to `$(go env GOMODCACHE)/github.com/thecodearcher/`. `go` lives at `/home/anders/.local/go/bin/go` if it is not on PATH; `sqlc`, `goose`, `golangci-lint` are in `~/go/bin`.

| Fact | Where |
| --- | --- |
| `limen.Hooks{Before, After []*limen.Hook}`, `limen.Hook{Run HookFunc, PathMatcher PathMatcherFunc}`, both `func(*limen.HookContext) bool`; a Before hook returning false stops the request. | `limen@…/hook.go:7-24` |
| `HookContext` getters: `Request()`, `RouteID()`, `StatusCode()`, `GetJSONBodyData()`, `GetJSONBodyValue(key)`, `GetAuthResult() *AuthenticationResult`, `GetResponse()`. The JSON body is parsed once (`parseAndStoreBody`) before hooks run and is available to Before AND After hooks. | `hook.go:50-98`, `router.go:224-260,302` |
| `limen.WithHTTPHooks(*limen.Hooks)` is the HTTP config option. | `http_config.go:163` |
| credential-password route IDs: `"signin"`, `"signup"`, `"passwords-request-reset"`, `"passwords-reset"`. Signup validates only `email`/`password`(/`username`) — extra body keys (`name`, `locale`) are NOT rejected (`ValidateRequest` never checks for unknown keys). | `plugins/credential-password@…/handlers.go:48-56,95-108`; `limen@…/validator.go:542-559` |
| The signup handler calls `responder.SessionResponse(w, r, core, result, nil)` even when auto-sign-in is OFF, and `SessionResponse` stores `result` on the response writer first thing — so an After hook on route `"signup"` always sees `GetAuthResult().User` (with `.ID`), regardless of `WithAutoSignInOnSignUp`. | `handlers.go:128-131`, `limen@…/response.go:128-132` |
| `credentialpassword.WithAutoSignInOnSignUp(bool)` exists. | `plugins/credential-password@…/types.go:77` |
| Password verify: `credentialpassword.Use(a).ComparePassword(password string, hash *string) (bool, error)`; returns `ErrPasswordNotSet` for a nil hash (OAuth-only user). Argon2id under the hood. | `api.go:11-39`, `password.go:24-36` |
| `POST /verify-email` (public, route id `"verify-email"`) reads `{"token"}`, calls `core.VerifyEmail` (sets `users.email_verified_at = now()` and deletes the token) and responds `200` with the JSON string `"email verified successfully"`. Bad token → `ErrEmailVerificationTokenInvalid`. `POST /email-verifications` (protected) resends to the caller's own email. | `limen@…/limen_handlers.go:37-38,85-113`, `email_verification.go:58-83` |
| `limen.User{ID any, Email string, Password *string, EmailVerifiedAt *time.Time}`; `GetSession` populates `EmailVerifiedAt` from the row. | `user.go:8-14,67` |
| OAuth: a NEW user gets `email_verified_at = now()` iff the provider reports `EmailVerified`; LINKING to an existing user by email performs no `EmailVerified` check at all. Google maps the `email_verified` claim. `oauth.WithGetUserInfo(func(ctx, providerName string, token *oauth.TokenResponse) (*oauth.ProviderUserInfo, error))` is consulted INSTEAD of `provider.GetUserInfo` when set — a fork-free interception point. `oauthgeneric.New` returns `oauth.Provider`. | `plugins/oauth@…/account_linker.go:13-42,123-142`, `authentication.go:133-141`, `config.go:96`, `plugins/oauth-google@…/provider.go:82-89`, `plugins/oauth-generic@…/provider.go:19` |
| `organization.WithHooks(organization.Hooks{…})` — value, not pointer. `BeforeCreateOrganization(ctx, user, *CreateOrganizationRequest) error` runs AFTER slug generation/normalization (so `request.Slug` is final); `BeforeUpdateOrganization(ctx, user, *Organization, *UpdateOrganizationRequest) error` likewise (`request.Slug` non-nil only when changing). Returning a `*limen.LimenError` makes the route answer with that status. | `plugins/organization@…/types.go:75-80,134`, `organizations.go:12-71,147-212` |
| Organization ids are `BIGSERIAL`; `SerializeModel` DELETES the `id` field unless it is a string, so `GET /organizations/` returns orgs WITHOUT ids — the SPA cannot switch by id through Limen. `SwitchOrganization(ctx, *limen.Session, identifier any)` exists on `organization.API` and returns a possibly re-issued cookie. | `limen@…/limen_core.go:68-85`, `plugins/organization@…/api.go:56-61`, `authorization.go:18-41` |
| Invitations: `POST /organizations/invitations` body `{email, role}` (both required; roles `owner`/`admin`/`member`); `GET /organizations/invitations/token/:token` embeds `organization` (id dropped, `name`+`slug` present); `POST /organizations/invitations/respond` body `{token, response: "accept"|"reject"}` string-compares the invitation email with the session user's email. | `handlers.go:43-47,310-360`, `invitations.go:13-18`, `constants.go:11-14,32-37` |
| Rate limiter: default key generator returns raw `X-Forwarded-For`, then `X-Real-IP`, then RemoteAddr; `limen.WithRateLimiterKeyGenerator(limen.RequestExtractorFn)` (`func(*http.Request) string`) replaces it; `limen.WithRateLimiterDisableForPaths(paths...)` — paths are joined onto the HTTP base path (`path.Join("/api/v1/auth", "/me")`), first matching rule wins, and a disabled rule passes the request through. Plugin rules: signin/signup 5 per 10 s, passwords 5 per 10 min; global default 100/min. | `utils.go:71-80,362-388`, `rate_limiter_config.go:27-35,114-128`, `rate_limiter.go:107-150`, `session_config.go:9`, `plugins/credential-password@…/handlers.go:27-41` |
| Exported helpers used below: `limen.NormalizeEmail`, `limen.NewLimenError(message string, status int, details any) *LimenError`, `limen.ErrRecordNotFound`, `organization.ErrMemberNotInOrganization`. | `utils.go:38`, `errors.go:51`, `plugins/organization@…/errors.go:22` |

## File map

| Path | Responsibility |
| --- | --- |
| `migrations/00009_user_profile.sql` (new) | `user_preferences` table |
| `internal/mailer/format.go` (new) | locale list + locale-aware date/time formatting |
| `internal/auth/profile.go` (new) | Profile/GetProfile/SetProfile/LocaleFor/MarkEmailVerified/DisplayName |
| `internal/auth/signup_hook.go` (new) | Limen After hook on `signup`: persist name + locale |
| `internal/auth/session.go` (modify) | `Session.EmailVerified`, gate in RequireSession/RequireStaff, `RequireSessionAllowUnverified`, `AuthMountGuard` |
| `internal/auth/auth.go` (modify) | hooks wiring, auto-sign-in off, transformer fields, localized mails, org hooks, OIDC guard, limiter options, slug cap |
| `internal/auth/cascade.go` (new) | `CascadeDeleteUser` (moved from internal/admin) |
| `internal/auth/account.go` (new) | `CheckOwnPassword`, `DeleteOwnAccount`, `ListUserOrganizations`, `SwitchOrganization` |
| `internal/auth/orgslug.go` (new) | `ValidateOrgSlug` + Limen org hooks |
| `internal/auth/oidc_guard.go` (new) | verified-email guard for the generic OIDC provider |
| `internal/clientip/clientip.go` (new) | `FromRequest(r, trustProxy)` (shared by httpserver and auth) |
| `internal/httpserver/account.go` (new) | `/api/v1/me*` handlers |
| `internal/httpserver/captcha.go` (new) | captcha middleware for the three hot auth routes |
| `internal/httpserver/server.go`, `ratelimit.go`, `testroutes.go` (modify) | wiring; seed route signs in after signup and marks verified |
| `internal/admin/users.go` (modify) | `DeleteUser` delegates to `auth.CascadeDeleteUser` |
| `internal/bookings/schemas.go` (modify) | `validateHandle` delegates to `auth.ValidateOrgSlug` |
| `web/src/lib/captcha.ts`, `web/src/lib/session-guard.ts` (new) | captcha capability hook; verified-session route guard |
| `web/src/api/auth.ts`, `polls.ts`, `bookings.ts` (modify) | new API functions, captcha params, locale plumbing |
| `web/src/components/auth/{TurnstileField,CredentialLoginForm,OidcButton,AcceptInvitationCard,DeleteAccountDialog}.tsx` | auth UI pieces |
| `web/src/routes/{login,signup,forgot-password,verify-email,settings}.tsx`, `accept-invitation/$id.tsx`, 8 guarded routes | pages |
| `web/src/components/layout/{UserMenu,LocaleSwitcher}.tsx` | org switcher; locale persistence |
| `web/messages/{en,nb}.json` | strings |
| `README.md`, `internal/auth/routes.txt` | docs |

---

### Task 1: Migration 00009 — `user_preferences`

**Files:**
- Create: `migrations/00009_user_profile.sql`
- Modify: `internal/db/db_test.go:11-20` (table list)
- Regenerate: `internal/polls/queries/models.go`, `internal/bookings/queries/models.go` (via `sqlc generate`)

**Interfaces:**
- Produces: table `user_preferences(user_id bigint PK → users.id ON DELETE CASCADE, locale text NOT NULL DEFAULT 'en', updated_at timestamptz NOT NULL DEFAULT now())`.

- [ ] **Step 1: Extend the migrations test**

In `internal/db/db_test.go`, change the table list inside `TestMigrationsCreateInfraTables` so the `users` line reads:

```go
		"users", "staff_users", "locked_users", "user_preferences",
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/db/ -run TestMigrationsCreateInfraTables`
Expected: FAIL with `table user_preferences missing (n=0, err=<nil>)`

- [ ] **Step 3: Write the migration**

Create `migrations/00009_user_profile.sql`:

```sql
-- +goose Up

-- Per-user application preferences. Ours, not Limen's — same reasoning as staff_users and
-- locked_users (migrations/00002_auth.sql, 00007_admin_locks.sql): extending Limen's generated
-- users table would couple our profile fields to the auth library's schema generator. Keyed on
-- Limen's bigint user id; ON DELETE CASCADE so internal/auth.CascadeDeleteUser never has to
-- know this table exists.
--
-- locale is the user's preferred UI/mail locale ("en" or "nb" — internal/mailer.SupportedLocales
-- is the authoritative list; the application validates before writing, so no CHECK here that
-- would need a migration every time a locale is added). Written at signup (from the request's
-- `locale` body field, the whenweall_locale cookie, or Accept-Language) and by PATCH /api/v1/me.
CREATE TABLE user_preferences (
  user_id    bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  locale     text NOT NULL DEFAULT 'en',
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS user_preferences;
```

- [ ] **Step 4: Regenerate sqlc models**

Run: `sqlc generate`
Expected: no output; `git status` shows `internal/polls/queries/models.go` and `internal/bookings/queries/models.go` modified (each gains a `UserPreference` struct; they may also pick up previously-missing `AdminAuditLog`/`LockedUser` structs — commit whatever sqlc emits).

- [ ] **Step 5: Run the test and the build**

Run: `go test ./internal/db/ -run TestMigrationsCreateInfraTables && go build ./...`
Expected: `ok  	github.com/refsdal/whenweall/internal/db`

- [ ] **Step 6: Commit**

```bash
git add migrations/00009_user_profile.sql internal/db/db_test.go internal/polls/queries internal/bookings/queries
git commit -m "feat(db): user_preferences table for per-user locale

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 2: `internal/mailer/format.go` — supported locales and locale-aware dates

**Files:**
- Create: `internal/mailer/format.go`, `internal/mailer/format_test.go`

**Interfaces:**
- Produces: `var SupportedLocales = []string{"en", "nb"}`; `func IsSupportedLocale(locale string) bool`; `func FormatDateTime(locale string, t time.Time, loc *time.Location) string`; `func FormatDate(locale string, t time.Time, loc *time.Location) string`; `func FormatTimeRange(locale string, start, end time.Time, loc *time.Location) string`. Plans C/D call these from `internal/polls/timers.go` (`optionLabelText`) and `internal/bookings/emails.go` (`bookingWhenText`).

- [ ] **Step 1: Write the failing tests**

Create `internal/mailer/format_test.go`:

```go
package mailer

import (
	"testing"
	"time"
)

// Pins the old frontend expectations (main:src/lib/__tests__/time.test.ts): en "Tue 1 Sep" /
// "18:30" / "– 19:30", nb weekday starting with "tir", 24-hour clock for BOTH locales.
func TestFormatDateTimePerLocale(t *testing.T) {
	oslo := time.FixedZone("CEST", 2*60*60) // no tzdata dependency in the test
	tue := time.Date(2026, time.September, 1, 16, 30, 0, 0, time.UTC) // 18:30 in Oslo
	mon := time.Date(2026, time.August, 31, 7, 5, 0, 0, time.UTC)     // 09:05 in Oslo

	cases := []struct {
		name   string
		locale string
		t      time.Time
		want   string
	}{
		{"en tuesday", "en", tue, "Tue 1 Sep, 18:30"},
		{"nb tuesday", "nb", tue, "tir. 1. sep., 18:30"},
		{"en monday", "en", mon, "Mon 31 Aug, 09:05"},
		{"nb monday", "nb", mon, "man. 31. aug., 09:05"},
		{"unknown locale falls back to en", "de", tue, "Tue 1 Sep, 18:30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDateTime(tc.locale, tc.t, oslo); got != tc.want {
				t.Errorf("FormatDateTime(%q) = %q, want %q", tc.locale, got, tc.want)
			}
		})
	}
}

func TestFormatDatePerLocale(t *testing.T) {
	oslo := time.FixedZone("CEST", 2*60*60)
	tue := time.Date(2026, time.September, 1, 16, 30, 0, 0, time.UTC)
	if got := FormatDate("en", tue, oslo); got != "Tue 1 Sep" {
		t.Errorf("FormatDate(en) = %q, want %q", got, "Tue 1 Sep")
	}
	if got := FormatDate("nb", tue, oslo); got != "tir. 1. sep." {
		t.Errorf("FormatDate(nb) = %q, want %q", got, "tir. 1. sep.")
	}
	// The zone matters: 23:30 UTC is already the next day in Oslo.
	late := time.Date(2026, time.September, 1, 23, 30, 0, 0, time.UTC)
	if got := FormatDate("en", late, oslo); got != "Wed 2 Sep" {
		t.Errorf("FormatDate(en, late) = %q, want %q", got, "Wed 2 Sep")
	}
}

func TestFormatTimeRange(t *testing.T) {
	oslo := time.FixedZone("CEST", 2*60*60)
	start := time.Date(2026, time.September, 1, 16, 30, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for _, locale := range SupportedLocales {
		if got := FormatTimeRange(locale, start, end, oslo); got != "18:30–19:30" {
			t.Errorf("FormatTimeRange(%q) = %q, want %q", locale, got, "18:30–19:30")
		}
	}
	// nil location means UTC.
	if got := FormatTimeRange("en", start, end, nil); got != "16:30–17:30" {
		t.Errorf("FormatTimeRange(nil loc) = %q, want %q", got, "16:30–17:30")
	}
}

func TestIsSupportedLocale(t *testing.T) {
	for _, l := range SupportedLocales {
		if !IsSupportedLocale(l) {
			t.Errorf("IsSupportedLocale(%q) = false", l)
		}
	}
	for _, l := range []string{"", "de", "EN", "nb-NO"} {
		if IsSupportedLocale(l) {
			t.Errorf("IsSupportedLocale(%q) = true, want false", l)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/mailer/ -run 'TestFormat|TestIsSupportedLocale'`
Expected: FAIL to compile — `undefined: FormatDateTime` (and the others).

- [ ] **Step 3: Implement**

Create `internal/mailer/format.go`:

```go
package mailer

import (
	"fmt"
	"time"
)

// SupportedLocales is the authoritative list of locales the application renders mail and UI in.
// internal/auth validates a user's stored locale against this; the web app's paraglide config
// (web/project.inlang/settings.json) must list the same set.
var SupportedLocales = []string{"en", "nb"}

// IsSupportedLocale reports whether locale is exactly one of SupportedLocales (case-sensitive,
// no region tags — the app stores bare "en"/"nb").
func IsSupportedLocale(locale string) bool {
	for _, l := range SupportedLocales {
		if l == locale {
			return true
		}
	}
	return false
}

// Norwegian short names, matching what Intl.DateTimeFormat("nb-NO", {weekday:"short"}) and
// {month:"short"} produced in the old frontend (main:src/lib/time.ts). Index by time.Weekday
// (Sunday = 0) and time.Month-1.
var nbWeekdays = [...]string{"søn.", "man.", "tir.", "ons.", "tor.", "fre.", "lør."}
var nbMonths = [...]string{"jan.", "feb.", "mar.", "apr.", "mai", "jun.", "jul.", "aug.", "sep.", "okt.", "nov.", "des."}

// inLocation converts t to loc, treating a nil loc as UTC.
func inLocation(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		return t.UTC()
	}
	return t.In(loc)
}

// FormatDate renders the calendar day of t (in loc) the way the old frontend's formatOptionLabel
// did: en "Tue 1 Sep", nb "tir. 1. sep.". Unknown locales render as en.
func FormatDate(locale string, t time.Time, loc *time.Location) string {
	lt := inLocation(t, loc)
	if locale == "nb" {
		return fmt.Sprintf("%s %d. %s", nbWeekdays[lt.Weekday()], lt.Day(), nbMonths[lt.Month()-1])
	}
	return lt.Format("Mon 2 Jan")
}

// FormatDateTime is FormatDate plus a 24-hour clock: en "Tue 1 Sep, 18:30", nb
// "tir. 1. sep., 18:30". Both locales use the 24-hour clock (the old hhmm helper always used
// en-GB hourCycle h23 regardless of locale).
func FormatDateTime(locale string, t time.Time, loc *time.Location) string {
	lt := inLocation(t, loc)
	return FormatDate(locale, lt, lt.Location()) + ", " + lt.Format("15:04")
}

// FormatTimeRange renders "18:30–19:30" (en dash) in loc; identical for every locale.
func FormatTimeRange(_ string, start, end time.Time, loc *time.Location) string {
	return inLocation(start, loc).Format("15:04") + "–" + inLocation(end, loc).Format("15:04")
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/mailer/`
Expected: `ok  	github.com/refsdal/whenweall/internal/mailer`

- [ ] **Step 5: Commit**

```bash
git add internal/mailer/format.go internal/mailer/format_test.go
git commit -m "feat(mailer): SupportedLocales and locale-aware date formatting

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 3: `internal/auth/profile.go` — Profile, GetProfile, SetProfile, LocaleFor, MarkEmailVerified

**Files:**
- Create: `internal/auth/profile.go`, `internal/auth/profile_test.go`

**Interfaces:**
- Consumes: `mailer.IsSupportedLocale` (Task 2), table `user_preferences` (Task 1), existing `nameFromEmail` (auth.go).
- Produces:
  ```go
  type Profile struct { UserID string; Name string; Locale string; EmailVerified bool }
  var ErrNoSuchUser = errors.New("auth: no such user")
  type ProfileValidationError struct { Field, Message string } // implements error
  func DisplayName(firstName, lastName, email string) string
  func (s *Service) GetProfile(ctx context.Context, userID string) (Profile, error)
  func (s *Service) SetProfile(ctx context.Context, userID string, name *string, locale *string) error
  func (s *Service) LocaleFor(ctx context.Context, userID string) string
  func (s *Service) MarkEmailVerified(ctx context.Context, email string) error
  func (s *Service) profileByEmail(ctx context.Context, email string) (Profile, error) // unexported, Task 4 uses it
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/auth/profile_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// signUp creates a credential user through Limen's own route and returns their stringified id —
// the shape every seam method takes. No session is needed for the profile methods.
func signUp(t *testing.T, ts *testService, email string) string {
	t.Helper()
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	return fmt.Sprint(lookupUserID(t, ts, email))
}

func TestGetProfileDefaults(t *testing.T) {
	ts := newTestService(t)
	userID := signUp(t, ts, "ada.lovelace@example.com")

	p, err := ts.svc.GetProfile(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.UserID != userID {
		t.Errorf("UserID = %q, want %q", p.UserID, userID)
	}
	if p.Name != "ada.lovelace" {
		t.Errorf("Name = %q, want the email local part %q", p.Name, "ada.lovelace")
	}
	if p.Locale != "en" {
		t.Errorf("Locale = %q, want %q (no preferences row yet)", p.Locale, "en")
	}
	if p.EmailVerified {
		t.Errorf("EmailVerified = true for a fresh signup, want false")
	}
}

func TestSetProfileRoundTrip(t *testing.T) {
	ts := newTestService(t)
	userID := signUp(t, ts, "profile@example.com")
	ctx := context.Background()

	name := "  Ada   Lovelace "
	locale := "nb"
	if err := ts.svc.SetProfile(ctx, userID, &name, &locale); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	p, err := ts.svc.GetProfile(ctx, userID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Name != "Ada Lovelace" {
		t.Errorf("Name = %q, want %q (whitespace collapsed)", p.Name, "Ada Lovelace")
	}
	if p.Locale != "nb" {
		t.Errorf("Locale = %q, want nb", p.Locale)
	}
	if got := ts.svc.LocaleFor(ctx, userID); got != "nb" {
		t.Errorf("LocaleFor = %q, want nb", got)
	}

	// nil means unchanged: a locale-only update must not blank the name.
	en := "en"
	if err := ts.svc.SetProfile(ctx, userID, nil, &en); err != nil {
		t.Fatalf("SetProfile(locale only): %v", err)
	}
	p, _ = ts.svc.GetProfile(ctx, userID)
	if p.Name != "Ada Lovelace" || p.Locale != "en" {
		t.Errorf("after locale-only update: Name=%q Locale=%q, want Ada Lovelace/en", p.Name, p.Locale)
	}

	// The stored split is first_name/last_name, so admin's composeUserName sees the same name.
	var first, last string
	if err := ts.svc.db.QueryRowContext(ctx, "SELECT first_name, last_name FROM users WHERE id = $1", lookupUserID(t, ts, "profile@example.com")).Scan(&first, &last); err != nil {
		t.Fatalf("reading first/last name: %v", err)
	}
	if first != "Ada" || last != "Lovelace" {
		t.Errorf("first_name/last_name = %q/%q, want Ada/Lovelace", first, last)
	}
}

func TestSetProfileValidation(t *testing.T) {
	ts := newTestService(t)
	userID := signUp(t, ts, "validate@example.com")
	ctx := context.Background()

	cases := []struct {
		name      string
		nameArg   *string
		localeArg *string
		wantField string
	}{
		{"blank name", ptr("   "), nil, "name"},
		{"name over 80 runes", ptr(strings.Repeat("å", 81)), nil, "name"},
		{"unsupported locale", nil, ptr("de"), "locale"},
		{"empty locale", nil, ptr(""), "locale"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ts.svc.SetProfile(ctx, userID, tc.nameArg, tc.localeArg)
			var verr *ProfileValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("SetProfile error = %v, want *ProfileValidationError", err)
			}
			if verr.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", verr.Field, tc.wantField)
			}
		})
	}

	// Exactly 80 runes is fine.
	ok := strings.Repeat("å", 80)
	if err := ts.svc.SetProfile(ctx, userID, &ok, nil); err != nil {
		t.Errorf("SetProfile(80 runes) = %v, want nil", err)
	}
}

func TestProfileUnknownUser(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	if _, err := ts.svc.GetProfile(ctx, "999999"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("GetProfile(unknown) = %v, want ErrNoSuchUser", err)
	}
	if _, err := ts.svc.GetProfile(ctx, "not-a-number"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("GetProfile(garbage) = %v, want ErrNoSuchUser", err)
	}
	name := "Nobody"
	if err := ts.svc.SetProfile(ctx, "999999", &name, nil); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SetProfile(unknown, name) = %v, want ErrNoSuchUser", err)
	}
	nb := "nb"
	if err := ts.svc.SetProfile(ctx, "999999", nil, &nb); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("SetProfile(unknown, locale) = %v, want ErrNoSuchUser", err)
	}
	if got := ts.svc.LocaleFor(ctx, "999999"); got != "en" {
		t.Errorf("LocaleFor(unknown) = %q, want en", got)
	}
	if err := ts.svc.MarkEmailVerified(ctx, "nobody@example.com"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("MarkEmailVerified(unknown) = %v, want ErrNoSuchUser", err)
	}
}

func TestMarkEmailVerified(t *testing.T) {
	ts := newTestService(t)
	email := "Verify.Me@Example.com"
	userID := signUp(t, ts, email)
	ctx := context.Background()

	// Accepts the un-normalized spelling the caller has, same as Limen's own lookups.
	if err := ts.svc.MarkEmailVerified(ctx, email); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	p, err := ts.svc.GetProfile(ctx, userID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if !p.EmailVerified {
		t.Error("EmailVerified = false after MarkEmailVerified")
	}
	// Idempotent.
	if err := ts.svc.MarkEmailVerified(ctx, email); err != nil {
		t.Errorf("second MarkEmailVerified: %v", err)
	}
}

func TestDisplayName(t *testing.T) {
	cases := []struct{ first, last, email, want string }{
		{"Ada", "Lovelace", "ada@example.com", "Ada Lovelace"},
		{"Ada", "", "ada@example.com", "Ada"},
		{"", "Lovelace", "ada@example.com", "Lovelace"},
		{"", "", "ada.l@example.com", "ada.l"},
		{" ", " ", "weird", "weird"},
	}
	for _, tc := range cases {
		if got := DisplayName(tc.first, tc.last, tc.email); got != tc.want {
			t.Errorf("DisplayName(%q,%q,%q) = %q, want %q", tc.first, tc.last, tc.email, got, tc.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'TestGetProfile|TestSetProfile|TestProfileUnknownUser|TestMarkEmailVerified|TestDisplayName'`
Expected: FAIL to compile — `ts.svc.GetProfile undefined`, `undefined: ErrNoSuchUser`, etc.

- [ ] **Step 3: Implement**

Create `internal/auth/profile.go`:

```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thecodearcher/limen"

	"github.com/refsdal/whenweall/internal/mailer"
)

// Profile is the seam's view of a user's editable account data plus the two facts every other
// package keeps needing about a user: what to call them and which locale to write to them in.
// Name never comes back empty (falls back to the email's local part) and Locale is always one of
// mailer.SupportedLocales (falls back to "en").
type Profile struct {
	UserID        string
	Name          string
	Locale        string
	EmailVerified bool
}

// ErrNoSuchUser is returned by the profile/account methods for an id or email that matches no
// users row (including a non-numeric id string).
var ErrNoSuchUser = errors.New("auth: no such user")

// ProfileValidationError reports a rejected SetProfile input. Field is "name" or "locale";
// internal/httpserver maps it to the standard 422 "invalid" envelope with Fields{Field: Message}.
type ProfileValidationError struct {
	Field   string
	Message string
}

func (e *ProfileValidationError) Error() string {
	return "auth: invalid " + e.Field + ": " + e.Message
}

// maxProfileNameRunes mirrors the signup form's own limit (web/src/routes/signup.tsx: "Keep your
// name under 80 characters").
const maxProfileNameRunes = 80

// DisplayName composes a user's display name from Limen's nullable first_name/last_name columns,
// falling back to the email's local part when both are blank (same rule as internal/admin's
// composeUserName and the displayName helpers in internal/polls and internal/bookings).
func DisplayName(firstName, lastName, email string) string {
	name := strings.TrimSpace(strings.TrimSpace(firstName) + " " + strings.TrimSpace(lastName))
	if name != "" {
		return name
	}
	return nameFromEmail(email)
}

// normalizeLocale returns locale if the mailer supports it, else "en" — Profile.Locale is never
// something a template can't render.
func normalizeLocale(locale string) string {
	if mailer.IsSupportedLocale(locale) {
		return locale
	}
	return "en"
}

// splitName stores "Ada Lovelace" as first_name "Ada" / last_name "Lovelace" (everything after
// the first space is the last name, so DisplayName reassembles it losslessly).
func splitName(name string) (first, last string) {
	parts := strings.SplitN(name, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

const profileSelect = `
	SELECT u.id, u.email, coalesce(u.first_name, ''), coalesce(u.last_name, ''),
	       u.email_verified_at IS NOT NULL, coalesce(p.locale, 'en')
	FROM users u
	LEFT JOIN user_preferences p ON p.user_id = u.id
	WHERE `

// GetProfile loads userID's Profile. ErrNoSuchUser for an unknown or non-numeric id.
func (s *Service) GetProfile(ctx context.Context, userID string) (Profile, error) {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return Profile{}, ErrNoSuchUser
	}
	return s.loadProfile(ctx, "u.id = $1", uid)
}

// profileByEmail is GetProfile keyed on the (normalized) email — for Limen's mail callbacks,
// which only ever hand this package an email address.
func (s *Service) profileByEmail(ctx context.Context, email string) (Profile, error) {
	return s.loadProfile(ctx, "u.email = $1", limen.NormalizeEmail(email))
}

func (s *Service) loadProfile(ctx context.Context, where string, arg any) (Profile, error) {
	var (
		id                 int64
		email, first, last string
		verified           bool
		locale             string
	)
	err := s.db.QueryRowContext(ctx, profileSelect+where, arg).Scan(&id, &email, &first, &last, &verified, &locale)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNoSuchUser
	}
	if err != nil {
		return Profile{}, fmt.Errorf("auth: loading profile: %w", err)
	}
	return Profile{
		UserID:        strconv.FormatInt(id, 10),
		Name:          DisplayName(first, last, email),
		Locale:        normalizeLocale(locale),
		EmailVerified: verified,
	}, nil
}

// SetProfile updates the parts of a profile the user may edit. nil means "leave unchanged".
// name is whitespace-collapsed and must be 1..80 runes; locale must be one of
// mailer.SupportedLocales. Both are validated before anything is written, so a request that fails
// validation changes nothing.
func (s *Service) SetProfile(ctx context.Context, userID string, name *string, locale *string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrNoSuchUser
	}

	var first, last string
	if name != nil {
		trimmed := strings.Join(strings.Fields(*name), " ")
		if trimmed == "" {
			return &ProfileValidationError{Field: "name", Message: "name is required"}
		}
		if utf8.RuneCountInString(trimmed) > maxProfileNameRunes {
			return &ProfileValidationError{Field: "name", Message: fmt.Sprintf("name must be at most %d characters", maxProfileNameRunes)}
		}
		first, last = splitName(trimmed)
	}
	if locale != nil && !mailer.IsSupportedLocale(*locale) {
		return &ProfileValidationError{Field: "locale", Message: "locale must be one of " + strings.Join(mailer.SupportedLocales, ", ")}
	}
	if name == nil && locale == nil {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin set-profile tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if name != nil {
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET first_name = $2, last_name = NULLIF($3, ''), updated_at = now() WHERE id = $1`,
			uid, first, last)
		if err != nil {
			return fmt.Errorf("auth: updating name: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoSuchUser
		}
	}
	if locale != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_preferences (user_id, locale, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (user_id) DO UPDATE SET locale = EXCLUDED.locale, updated_at = now()
		`, uid, *locale); err != nil {
			// 23503 = foreign_key_violation: no users row for uid.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return ErrNoSuchUser
			}
			return fmt.Errorf("auth: upserting locale: %w", err)
		}
	}
	return tx.Commit()
}

// LocaleFor is the cheap "which locale do I write this user in" helper for mail senders: the
// stored locale, or "en" when the user is unknown, has no preference, or the lookup fails (a mail
// in the wrong language beats no mail).
func (s *Service) LocaleFor(ctx context.Context, userID string) string {
	p, err := s.GetProfile(ctx, userID)
	if err != nil {
		return "en"
	}
	return p.Locale
}

// MarkEmailVerified sets email_verified_at (if not already set) for the user with this email.
// Used by the e2e seed route (internal/httpserver/testroutes.go) and by tests that need a usable
// account without driving the mailed token round trip; production code never calls it — Limen's
// POST /verify-email is the real path.
func (s *Service) MarkEmailVerified(ctx context.Context, email string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now()
		WHERE email = $1
	`, limen.NormalizeEmail(email))
	if err != nil {
		return fmt.Errorf("auth: marking %q verified: %w", email, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchUser
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/auth/ -run 'TestGetProfile|TestSetProfile|TestProfileUnknownUser|TestMarkEmailVerified|TestDisplayName'`
Expected: PASS for all six.

- [ ] **Step 5: Lint and commit**

Run: `go vet ./internal/auth/ && golangci-lint run ./internal/auth/`
Expected: no findings.

```bash
git add internal/auth/profile.go internal/auth/profile_test.go
git commit -m "feat(auth): Profile with display name, locale and verified flag

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 4: Signup persists name + locale; `/me` carries profile fields; auth mails use them

**Files:**
- Create: `internal/auth/signup_hook.go`
- Modify: `internal/auth/auth.go` (`httpConfigOptions`, `sessionTransformer`, `lookupStaffForSessionResponse` → `lookupSessionExtras`, `enqueueTokenMail`, `enqueueInviteMail`)
- Test: `internal/auth/signup_hook_test.go`

**Interfaces:**
- Consumes: `SetProfile`, `profileByEmail`, `GetProfile`, `DisplayName` (Task 3); Limen `WithHTTPHooks`, `HookContext` (see facts table).
- Produces: `POST /api/v1/auth/signup/credential` accepts optional `name` and `locale` body fields (locale falls back to the `whenweall_locale` cookie, then `Accept-Language`, then `en`); `/me`, signin and signup responses carry `user.name`, `user.locale`, `user.emailVerified`, `user.hasPassword`; `verify_email`/`reset_password`/`org_invite` mails carry `Data.Name`/`Data.InviterName` from the profile and `Data.Locale`.

- [ ] **Step 1: Write the failing tests**

Create `internal/auth/signup_hook_test.go`:

```go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// postSignup is postJSON with extra request decoration (cookies / headers) — the signup hook reads
// the locale off the request, not only off the body.
func postSignup(t *testing.T, ts *testService, body map[string]any, decorate func(*http.Request)) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.url("/api/v1/auth/signup/credential"), strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if decorate != nil {
		decorate(req)
	}
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatalf("POST signup: %v", err)
	}
	return resp
}

func TestSignupStoresNameAndLocaleFromBody(t *testing.T) {
	ts := newTestService(t)
	email := "named@example.com"

	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": "Ada Lovelace", "locale": "nb",
	}, nil), "signup")

	p, err := ts.svc.GetProfile(context.Background(), lookupUserIDString(t, ts, email))
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Name != "Ada Lovelace" {
		t.Errorf("Name = %q, want Ada Lovelace", p.Name)
	}
	if p.Locale != "nb" {
		t.Errorf("Locale = %q, want nb", p.Locale)
	}

	// The verification mail enqueued by that same signup greets by name, in the user's locale.
	msg, ok := ts.mail.find("verify_email")
	if !ok {
		t.Fatal("no verify_email mail captured")
	}
	if got, _ := msg.Data["Name"].(string); got != "Ada Lovelace" {
		t.Errorf("verify_email Data.Name = %q, want Ada Lovelace", got)
	}
	if got, _ := msg.Data["Locale"].(string); got != "nb" {
		t.Errorf("verify_email Data.Locale = %q, want nb", got)
	}
}

func TestSignupLocaleFallsBackToCookieThenAcceptLanguage(t *testing.T) {
	ts := newTestService(t)

	cases := []struct {
		name     string
		email    string
		decorate func(*http.Request)
		want     string
	}{
		{"cookie", "cookie-nb@example.com", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "whenweall_locale", Value: "nb"})
		}, "nb"},
		{"accept-language", "header-nb@example.com", func(r *http.Request) {
			r.Header.Set("Accept-Language", "nb-NO,nb;q=0.9,en;q=0.8")
		}, "nb"},
		{"unsupported everywhere", "de@example.com", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "whenweall_locale", Value: "de"})
			r.Header.Set("Accept-Language", "de-DE,fr;q=0.5")
		}, "en"},
		{"nothing", "plain@example.com", nil, "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireStatus2xx(t, postSignup(t, ts, map[string]any{"email": tc.email, "password": signupPassword}, tc.decorate), "signup")
			if got := ts.svc.LocaleFor(context.Background(), lookupUserIDString(t, ts, tc.email)); got != tc.want {
				t.Errorf("LocaleFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSignupIgnoresInvalidNameButKeepsLocale(t *testing.T) {
	ts := newTestService(t)
	email := "longname@example.com"

	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": strings.Repeat("x", 200), "locale": "nb",
	}, nil), "signup")

	p, err := ts.svc.GetProfile(context.Background(), lookupUserIDString(t, ts, email))
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Name != "longname" {
		t.Errorf("Name = %q, want the email local part (invalid name dropped)", p.Name)
	}
	if p.Locale != "nb" {
		t.Errorf("Locale = %q, want nb (a bad name must not cost the locale)", p.Locale)
	}
}

func TestSessionResponseCarriesProfileFields(t *testing.T) {
	ts := newTestService(t)
	email := "session-fields@example.com"

	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": "Grace Hopper", "locale": "nb",
	}, nil), "signup")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": signupPassword,
	}), "signin")

	me := decodeJSON(t, ts.get(t, "/api/v1/auth/me"))
	user, _ := me["user"].(map[string]any)
	if got, _ := user["name"].(string); got != "Grace Hopper" {
		t.Errorf("user.name = %q, want Grace Hopper", got)
	}
	if got, _ := user["locale"].(string); got != "nb" {
		t.Errorf("user.locale = %q, want nb", got)
	}
	if verified, ok := user["emailVerified"].(bool); !ok || verified {
		t.Errorf("user.emailVerified = %#v, want false", user["emailVerified"])
	}
	if hasPassword, _ := user["hasPassword"].(bool); !hasPassword {
		t.Errorf("user.hasPassword = %#v, want true for a credential signup", user["hasPassword"])
	}
	if _, leaked := user["password"]; leaked {
		t.Error("user.password must never be in the payload")
	}
}

func TestPasswordResetMailUsesProfileNameAndLocale(t *testing.T) {
	ts := newTestService(t)
	email := "reset-nb@example.com"
	requireStatus2xx(t, postSignup(t, ts, map[string]any{
		"email": email, "password": signupPassword, "name": "Kari Nordmann", "locale": "nb",
	}, nil), "signup")

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/passwords/request-reset", map[string]any{"email": email}), "request-reset")
	msg, ok := ts.mail.find("reset_password")
	if !ok {
		t.Fatal("no reset_password mail captured")
	}
	if got, _ := msg.Data["Name"].(string); got != "Kari Nordmann" {
		t.Errorf("Data.Name = %q, want Kari Nordmann", got)
	}
	if got, _ := msg.Data["Locale"].(string); got != "nb" {
		t.Errorf("Data.Locale = %q, want nb", got)
	}
}

func TestRequestLocaleParsing(t *testing.T) {
	mk := func(cookie, accept string) *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "http://x/signup", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "whenweall_locale", Value: cookie})
		}
		if accept != "" {
			r.Header.Set("Accept-Language", accept)
		}
		return r
	}
	cases := []struct {
		body   any
		cookie string
		accept string
		want   string
	}{
		{"nb", "", "", "nb"},
		{"NB", "", "", "en"}, // exact match only
		{nil, "nb", "en", "nb"},
		{nil, "", "en-GB,en;q=0.9,nb;q=0.8", "en"},
		{nil, "", "nb-NO", "nb"},
		{nil, "", " fr , nb ;q=0.2", "nb"},
		{nil, "", "*", "en"},
		{42, "", "", "en"},
	}
	for _, tc := range cases {
		if got := requestLocale(mk(tc.cookie, tc.accept), tc.body); got != tc.want {
			t.Errorf("requestLocale(body=%v cookie=%q accept=%q) = %q, want %q", tc.body, tc.cookie, tc.accept, got, tc.want)
		}
	}
}

// lookupUserIDString is lookupUserID (personal_org_test.go) in the seam's string-id shape.
func lookupUserIDString(t *testing.T, ts *testService, email string) string {
	t.Helper()
	return fmt.Sprint(lookupUserID(t, ts, email))
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'TestSignup|TestSessionResponseCarriesProfileFields|TestPasswordResetMailUsesProfile|TestRequestLocaleParsing'`
Expected: FAIL — `undefined: requestLocale` (compile), and after stubbing, the profile assertions fail (`Name = "named"`, `Locale = "en"`, `user.name` missing).

- [ ] **Step 3: Implement the hook**

Create `internal/auth/signup_hook.go`:

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/thecodearcher/limen"

	"github.com/refsdal/whenweall/internal/mailer"
)

// localeCookieName is paraglide's cookie (web/src/paraglide/runtime.js: cookieName) — the SPA sets
// it on every locale switch, so a guest who picked Norwegian before signing up carries it here.
const localeCookieName = "whenweall_locale"

// signupHooks builds the Limen HTTP hooks: one After hook on credential-password's "signup"
// route. Body access is confirmed against the pinned source (router.go parses the JSON body once
// before hooks run; HookContext.GetJSONBodyValue reads it), and so is auth-result access:
// SignUpWithCredentialAndPassword calls Responder.SessionResponse in BOTH the auto-sign-in and
// no-auto-sign-in branches, and SessionResponse stores the AuthenticationResult on the response
// writer before doing anything else — so GetAuthResult() is populated for every successful
// signup regardless of WithAutoSignInOnSignUp. (The earlier personal-org attempt at a hook failed
// for the OAuth callback, which redirects instead of calling SessionResponse; this hook is only
// ever matched to "signup", where that problem does not exist.)
func (s *Service) signupHooks() *limen.Hooks {
	return &limen.Hooks{
		After: []*limen.Hook{{
			PathMatcher: func(hc *limen.HookContext) bool { return hc.RouteID() == "signup" },
			Run:         s.afterSignup,
		}},
	}
}

// afterSignup persists the two profile fields Limen's signup handler ignores — `name` (the form's
// required Name field) and the locale — for the user the request just created. It never fails
// the request: the account exists by the time this runs, and a profile hiccup is not a reason to
// tell the user their signup failed. Always returns true (After hooks' return value is ignored
// anyway).
func (s *Service) afterSignup(hc *limen.HookContext) bool {
	result := hc.GetAuthResult()
	if result == nil || result.User == nil {
		return true // signup failed (validation, duplicate email, ...) — nothing was created
	}
	userID := fmt.Sprint(result.User.ID)

	var name *string
	if raw, _ := hc.GetJSONBodyValue("name").(string); strings.TrimSpace(raw) != "" {
		name = &raw
	}
	locale := requestLocale(hc.Request(), hc.GetJSONBodyValue("locale"))

	// context.Background(): same reasoning as sendMail — the request context may be on its way
	// out, and this is bookkeeping for a row that already exists.
	ctx := context.Background()
	err := s.SetProfile(ctx, userID, name, &locale)
	var verr *ProfileValidationError
	if errors.As(err, &verr) && name != nil {
		// The name was unusable (too long, say). Drop it, keep the locale.
		s.logger.Warn("auth: signup name rejected; storing locale only", "user_id", userID, "error", err)
		err = s.SetProfile(ctx, userID, nil, &locale)
	}
	if err != nil {
		s.logger.Error("auth: storing signup profile failed", "user_id", userID, "error", err)
	}
	return true
}

// requestLocale picks the signup's locale: an explicit supported `locale` body value first, then
// the whenweall_locale cookie, then the first supported language in Accept-Language (base tag,
// so "nb-NO" counts as "nb"), else "en". Only exact members of mailer.SupportedLocales are ever
// returned.
func requestLocale(r *http.Request, bodyLocale any) string {
	if l, ok := bodyLocale.(string); ok && mailer.IsSupportedLocale(l) {
		return l
	}
	if r != nil {
		if c, err := r.Cookie(localeCookieName); err == nil && mailer.IsSupportedLocale(c.Value) {
			return c.Value
		}
		if l := acceptLanguageLocale(r.Header.Get("Accept-Language")); l != "" {
			return l
		}
	}
	return "en"
}

// acceptLanguageLocale returns the first supported base language in an Accept-Language header
// ("nb-NO,nb;q=0.9,en;q=0.8" -> "nb"), or "" when none is supported. Quality values are ignored
// beyond list order, which is how browsers order the list anyway.
func acceptLanguageLocale(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag == "" || tag == "*" {
			continue
		}
		base := strings.ToLower(strings.SplitN(tag, "-", 2)[0])
		if mailer.IsSupportedLocale(base) {
			return base
		}
	}
	return ""
}
```

- [ ] **Step 4: Wire the hook, extend the session payload, localize the mails (auth.go)**

In `httpConfigOptions`, replace the whole `// No limen.WithHTTPHooks here on purpose: …` comment (the eight comment lines after `limen.WithHTTPSessionTransformer(s.sessionTransformer),`) with:

```go
		// After hook on credential-password's "signup" route: stores the display name and locale
		// Limen's own handler ignores (see signup_hook.go for why body/auth-result access is safe
		// there — and why an earlier hook-based attempt at the personal-org invariant was NOT:
		// the OAuth callback redirects without calling SessionResponse, so it never had an auth
		// result to read; the personal org therefore stays lazily enforced in resolveSession).
		limen.WithHTTPHooks(s.signupHooks()),
```

Replace `sessionTransformer` and `lookupStaffForSessionResponse` (auth.go:281-325) with:

```go
// sessionTransformer implements limen.SessionTransformer (registered via
// limen.WithHTTPSessionTransformer in buildLimenConfig), so it runs on every signin/signup/me
// (and oauth) response that goes through Limen's Responder.SessionResponse.
//
// It exists because Limen's default user serialization never includes a usable id: UserSchema.Serialize
// deletes the id column outright (see the pinned source's user.go), and even without that,
// LimenCore.SerializeModel drops whatever's left of the id field unless it happens to already be a
// string — ours is a BIGSERIAL, so it comes back as an int64 and gets deleted either way. The
// frontend's self-ownership checks (e.g. "is this my own poll") need the user's id in the /me
// payload, so this rebuilds the payload from user (Limen hands us result.User.Raw(), the same
// unfiltered row resolveSession above scans user_id off of) instead of Limen's own serialization:
// the password column is stripped, and the id is added back as a Go string — matching the
// convention used everywhere else in this seam (fmt.Sprint(user.ID), see the users.id comment in
// migrations/00002_auth.sql) rather than leaving it as whatever numeric type the driver returned.
//
// It also adds what the SPA needs to render an account without a second round trip: "isStaff"
// (staff_users), "locale" (user_preferences, "en" when absent), "name" (DisplayName over
// first_name/last_name/email), "emailVerified" (email_verified_at IS NOT NULL — the gate in
// session.go keys on the same fact) and "hasPassword" (whether a credential exists — the settings
// page's delete-account dialog asks for the current password only when there is one).
func (s *Service) sessionTransformer(user map[string]any, _ *limen.SessionResult) (map[string]any, error) {
	payload := maps.Clone(user)
	payload["hasPassword"] = user["password"] != nil
	delete(payload, "password")

	rawID := user["id"]
	email, _ := user["email"].(string)
	first, _ := user["first_name"].(string)
	last, _ := user["last_name"].(string)
	staff, locale := s.lookupSessionExtras(rawID)

	payload["id"] = fmt.Sprint(rawID)
	payload["isStaff"] = staff
	payload["locale"] = locale
	payload["name"] = DisplayName(first, last, email)
	payload["emailVerified"] = user["email_verified_at"] != nil

	return map[string]any{"user": payload}, nil
}

// lookupSessionExtras is sessionTransformer's staff_users + user_preferences read, in one round
// trip. Unlike resolveSession's combined query, this can't take a request context —
// limen.SessionTransformer's signature carries none (Limen calls it outside any request scope) —
// so it issues its own query against context.Background(). Fails safe (false, "en") on error: a
// session response is not the place to turn a lookup hiccup into a broken login, and RequireStaff
// (session.go) is the actual gate on anything staff-only regardless of what this reports.
func (s *Service) lookupSessionExtras(userID any) (staff bool, locale string) {
	if err := s.db.QueryRowContext(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM staff_users WHERE user_id = $1),
		       coalesce((SELECT locale FROM user_preferences WHERE user_id = $1), 'en')
	`, userID).Scan(&staff, &locale); err != nil {
		s.logger.Error("auth: staff/locale lookup failed in session transformer; defaulting", "user_id", fmt.Sprint(userID), "error", err)
		return false, "en"
	}
	return staff, normalizeLocale(locale)
}
```

Replace `enqueueTokenMail` (auth.go:481-493) with:

```go
// enqueueTokenMail builds the reset-password/verify-email link (Limen only ever passes the
// callback a bare token, not a full URL) and enqueues it under templateName. path is the SPA
// route that will read ?token= and complete the flow. Name and Locale come from the user's
// profile — Limen hands over only the email, so the profile is looked up by it; a lookup failure
// falls back to the email's local part and "en" rather than dropping the mail.
func (s *Service) enqueueTokenMail(email, templateName, path, token string) {
	name, locale := nameFromEmail(email), "en"
	if p, err := s.profileByEmail(context.Background(), email); err == nil {
		name, locale = p.Name, p.Locale
	}
	s.sendMail(mailer.Message{
		To:       email,
		Template: templateName,
		Data: map[string]any{
			"URL":    s.cfg.AppURL + path + "?token=" + token,
			"Name":   name,
			"Locale": locale,
		},
	})
}
```

Replace `enqueueInviteMail` (auth.go:495-518) with:

```go
// enqueueInviteMail sends the org_invite mail. The link points at the SPA's accept-invitation
// page (not a Limen backend route — there is no such route; the SPA reads the token from the
// path and calls organization's respond-to-invitation API itself). InviterName is the inviter's
// stored display name; Locale is the invitee's own stored locale when they already have an
// account, else the inviter's (a Norwegian team inviting a colleague is the best guess available
// for someone we know nothing about yet).
func (s *Service) enqueueInviteMail(ctx context.Context, d *organization.SendInvitationMailData) {
	if d == nil || d.Invitation == nil || d.Organization == nil {
		s.logger.Error("auth: org invite mail callback missing invitation or organization data")
		return
	}

	inviterName, locale := "", "en"
	if d.Inviter != nil {
		inviterName = nameFromEmail(d.Inviter.Email)
		if p, err := s.GetProfile(ctx, fmt.Sprint(d.Inviter.ID)); err == nil {
			inviterName, locale = p.Name, p.Locale
		}
	}
	if p, err := s.profileByEmail(ctx, d.Invitation.Email); err == nil {
		locale = p.Locale
	}

	s.sendMailCtx(ctx, mailer.Message{
		To:       d.Invitation.Email,
		Template: "org_invite",
		Data: map[string]any{
			"URL":         s.cfg.AppURL + "/accept-invitation/" + d.Invitation.Token,
			"InviterName": inviterName,
			"OrgName":     d.Organization.Name,
			"Locale":      locale,
		},
	})
}
```

- [ ] **Step 5: Run the auth tests**

Run: `go test ./internal/auth/`
Expected: `ok` — every existing test still passes (auto-sign-in is still on in this task) plus the six new ones.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/signup_hook.go internal/auth/signup_hook_test.go internal/auth/auth.go
git commit -m "feat(auth): persist signup name+locale, expose profile on /me, localize auth mails

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 5: The email-verification gate (both layers), auto-sign-in off, verify-token round trip pinned

**Files:**
- Modify: `internal/auth/session.go` (Session struct, resolveSession, RequireSession, RequireStaff, `LockedSessionMiddleware` → `AuthMountGuard`), `internal/auth/auth.go:125-140` (`WithAutoSignInOnSignUp(false)`), `internal/httpserver/server.go:97-125`, `internal/httpserver/testroutes.go` (seed: sign in after signup, mark verified, doc comments)
- Test: `internal/auth/verification_test.go` (new); adjust `internal/auth/auth_test.go`, `internal/auth/personal_org_test.go`, `internal/admin/users_test.go:481-485`, `internal/admin/handlers_test.go:74-81`

**Interfaces:**
- Consumes: `MarkEmailVerified` (Task 3).
- Produces: `Session.EmailVerified bool`; `RequireSession`/`RequireStaff` (and therefore `httpserver.WithOrgSession`) answer `403 {"error":{"code":"email_unverified"}}` for an unverified session; `(*Service).RequireSessionAllowUnverified(next http.HandlerFunc) http.HandlerFunc` (the old RequireSession behaviour — Task 7 uses it for PATCH/DELETE /me); `(*Service).AuthMountGuard(next http.Handler) http.Handler` replaces `LockedSessionMiddleware` and additionally blocks an unverified session on every Limen route except `GET /me`, `POST /signout`, `POST /verify-email`, `POST /email-verifications` and the session-less credential routes (`POST /signin/credential`, `POST /signup/credential`, `POST /passwords/request-reset`, `POST /passwords/reset`); signup no longer mints a session.

- [ ] **Step 1: Write the failing gate test**

Create `internal/auth/verification_test.go`:

```go
package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// requireErrorCode asserts resp is `status` with our envelope's error.code == code.
func requireErrorCode(t *testing.T, resp *http.Response, status int, code, what string) {
	t.Helper()
	if resp.StatusCode != status {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("%s: status %d, want %d: %s", what, resp.StatusCode, status, body)
	}
	body := decodeJSON(t, resp)
	errObj, _ := body["error"].(map[string]any)
	if got, _ := errObj["code"].(string); got != code {
		t.Fatalf("%s: error.code = %q, want %q (body %+v)", what, got, code, body)
	}
}

// verifyToken extracts the token from a captured verify_email mail's URL.
func verifyToken(t *testing.T, ts *testService, index int) string {
	t.Helper()
	var seen int
	for _, m := range ts.mail.all() {
		if m.Template != "verify_email" {
			continue
		}
		if seen == index {
			url, _ := m.Data["URL"].(string)
			const marker = "/verify-email?token="
			i := strings.Index(url, marker)
			if i < 0 || url[i+len(marker):] == "" {
				t.Fatalf("verify_email URL %q carries no token", url)
			}
			return url[i+len(marker):]
		}
		seen++
	}
	t.Fatalf("no verify_email mail at index %d (have %d)", index, seen)
	return ""
}

// TestEmailVerificationGate re-expresses the old auth.workers.test.ts assertion ("sign-in is
// blocked until the verification token is consumed") on this stack: a fresh signup mints no
// session, signing in works but every gated surface answers 403 email_unverified until Limen's
// POST /verify-email consumes the mailed token, after which the same session passes.
func TestEmailVerificationGate(t *testing.T) {
	ts := newTestService(t)
	email := "gate@example.com"

	signupResp := ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": signupPassword,
	})
	if signupResp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(signupResp.Body)
		t.Fatalf("signup: status %d: %s", signupResp.StatusCode, body)
	}
	for _, c := range signupResp.Header.Values("Set-Cookie") {
		if strings.Contains(c, "limen_session=") {
			t.Fatalf("signup set a session cookie (%q); auto sign-in must be off", c)
		}
	}
	_ = signupResp.Body.Close()

	// Signing in is allowed (the user needs a session to resend the mail) …
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": signupPassword,
	}), "signin")

	// … but nothing gated is.
	requireErrorCode(t, ts.get(t, "/probe/session"), http.StatusForbidden, "email_unverified", "RequireSession before verify")
	requireErrorCode(t, ts.get(t, "/api/v1/auth/organizations/active"), http.StatusForbidden, "email_unverified", "Limen mount before verify")
	if err := ts.svc.MakeStaff(context.Background(), email); err != nil {
		t.Fatalf("MakeStaff: %v", err)
	}
	requireErrorCode(t, ts.get(t, "/probe/staff"), http.StatusForbidden, "email_unverified", "RequireStaff before verify")

	// The exemptions the SPA needs to get out of this state.
	requireStatus2xx(t, ts.get(t, "/api/v1/auth/me"), "GET /me while unverified")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/email-verifications", map[string]any{}), "resend while unverified")
	second := verifyToken(t, ts, 1) // the resend enqueued a second verify_email mail
	if second == "" {
		t.Fatal("resend produced no token")
	}

	// And a wrong token does nothing.
	bad := ts.postJSON(t, "/api/v1/auth/verify-email", map[string]any{"token": "nope"})
	if bad.StatusCode/100 == 2 {
		t.Fatal("POST /verify-email accepted a bogus token")
	}
	_ = bad.Body.Close()

	// Consume the original token (Limen's own route; the SPA's /verify-email page does exactly this).
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/verify-email", map[string]any{
		"token": verifyToken(t, ts, 0),
	}), "verify-email")

	var verified bool
	if err := ts.svc.db.QueryRowContext(context.Background(),
		"SELECT email_verified_at IS NOT NULL FROM users WHERE email = $1", email).Scan(&verified); err != nil {
		t.Fatalf("reading email_verified_at: %v", err)
	}
	if !verified {
		t.Fatal("email_verified_at still NULL after POST /verify-email")
	}

	// Same session, now allowed everywhere — the gate reads the row on every request.
	requireStatus2xx(t, ts.get(t, "/probe/session"), "RequireSession after verify")
	requireStatus2xx(t, ts.get(t, "/probe/staff"), "RequireStaff after verify")
	requireStatus2xx(t, ts.get(t, "/api/v1/auth/organizations/active"), "Limen mount after verify")

	probe := decodeJSON(t, ts.get(t, "/probe"))
	if v, _ := probe["EmailVerified"].(bool); !v {
		t.Errorf("Session.EmailVerified = false after verify: %+v", probe)
	}
}

// TestUnverifiedSessionIsStillASession: FromContext (not RequireSession) keeps reporting the user —
// handlers that want to treat unverified callers specially can — and RequireSessionAllowUnverified
// lets the two account routes that must work before verification (PATCH/DELETE /api/v1/me) through.
func TestUnverifiedSessionIsStillASession(t *testing.T) {
	ts := newTestService(t)
	email := "still-a-session@example.com"
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": signupPassword,
	}), "signup")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": signupPassword,
	}), "signin")

	probe := decodeJSON(t, ts.get(t, "/probe"))
	if anon, _ := probe["anonymous"].(bool); anon {
		t.Fatalf("probe reported anonymous for an unverified session: %+v", probe)
	}
	if v, _ := probe["EmailVerified"].(bool); v {
		t.Errorf("Session.EmailVerified = true for a fresh signup")
	}
	requireStatus2xx(t, ts.get(t, "/probe/session-unverified-ok"), "RequireSessionAllowUnverified")
}
```

Add the extra probe route to `newTestServiceWithConfig` in `auth_test.go`, right after the `/probe/staff` registration:

```go
	mux.HandleFunc("/probe/session-unverified-ok", svc.RequireSessionAllowUnverified(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'TestEmailVerificationGate|TestUnverifiedSessionIsStillASession'`
Expected: FAIL to compile (`RequireSessionAllowUnverified undefined`); after a stub, `signup set a session cookie`.

- [ ] **Step 3: Implement the gate in session.go**

Change the `Session` struct to:

```go
type Session struct {
	UserID      string
	Email       string
	ActiveOrgID string // "" when the user has no active organization
	Staff       bool
	// EmailVerified mirrors users.email_verified_at IS NOT NULL. RequireSession/RequireStaff (and
	// AuthMountGuard for Limen's own routes) refuse an unverified session with 403
	// email_unverified; RequireSessionAllowUnverified is the explicit opt-out for the two account
	// routes that must work before verification (PATCH/DELETE /api/v1/me).
	EmailVerified bool
}
```

In `resolveSession`, change the `sess := &Session{…}` literal to:

```go
	sess := &Session{
		UserID:        fmt.Sprint(validated.User.ID),
		Email:         validated.User.Email,
		Staff:         staff,
		EmailVerified: validated.User.EmailVerifiedAt != nil,
	}
```

Replace `RequireSession` and `RequireStaff` (session.go:248-276) with:

```go
// writeUnverified is the shared 403 for a session whose email is not yet verified. The SPA maps
// the code to its verify-email flow (web/src/lib/session-guard.ts redirects there before a route
// ever hits this; this is the server-side truth behind that redirect).
func writeUnverified(w http.ResponseWriter) {
	writeErrorEnvelope(w, http.StatusForbidden, "email_unverified", "email address not verified")
}

// RequireSession rejects an anonymous request with 401 {"error":{"code":"unauthenticated",...}}
// and a session whose email is unverified with 403 {"error":{"code":"email_unverified",...}}
// before calling next; a verified session passes through unchanged (next reads the Session back
// out via FromContext same as any other handler). Unverified accounts cannot use the app — this
// is the one gate every domain handler inherits (httpserver.WithOrgSession is built on it).
func (s *Service) RequireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := FromContext(r.Context())
		if !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if !sess.EmailVerified {
			writeUnverified(w)
			return
		}
		next(w, r)
	}
}

// RequireSessionAllowUnverified is RequireSession without the verification gate: 401 for an
// anonymous request, otherwise next. Only for routes an unverified user legitimately needs —
// setting their locale, deleting the account they just made — never for anything that reads or
// writes shared content.
func (s *Service) RequireSessionAllowUnverified(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r)
	}
}

// RequireStaff rejects an anonymous request with 401, an unverified one with 403 email_unverified,
// and a non-staff one with 403 forbidden, all as {"error":{"code":...}}.
func (s *Service) RequireStaff(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := FromContext(r.Context())
		if !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if !sess.EmailVerified {
			writeUnverified(w)
			return
		}
		if !sess.Staff {
			writeErrorEnvelope(w, http.StatusForbidden, "forbidden", "staff access required")
			return
		}
		next(w, r)
	}
}
```

Replace `authMountSignoutMethodAndPath` and `LockedSessionMiddleware` (session.go:173-227) with:

```go
// authMountSignoutMethodAndPath is the one exception AuthMountGuard carves out of the auth mount
// for a LOCKED user: signing out. A locked user has no legitimate reason to reach any other Limen
// route, but blocking signout too would leave them holding a cookie they can never clear.
const authMountSignoutMethodAndPath = "POST /api/v1/auth/signout"

// authMountUnverifiedAllowed lists the Limen routes an UNVERIFIED (but valid) session may still
// reach: reading itself, signing out, and completing/resending verification — plus the
// credential routes that never depend on the caller's session at all (a browser that still
// carries an unverified session cookie must be able to sign in as someone else or reset a
// password). Everything else under /api/v1/auth/ — organizations, invitations, oauth linking,
// password change — is refused with 403 email_unverified until POST /verify-email has run.
var authMountUnverifiedAllowed = map[string]struct{}{
	"GET /api/v1/auth/me":                       {},
	"POST /api/v1/auth/signout":                 {},
	"POST /api/v1/auth/verify-email":            {},
	"POST /api/v1/auth/email-verifications":     {},
	"POST /api/v1/auth/signin/credential":       {},
	"POST /api/v1/auth/signup/credential":       {},
	"POST /api/v1/auth/passwords/request-reset": {},
	"POST /api/v1/auth/passwords/reset":         {},
}

// AuthMountGuard wraps Limen's own handler — mounted at /api/v1/auth/ by internal/httpserver — and
// applies the two per-user restrictions Limen itself knows nothing about, in this order:
//
//  1. Lock (locked_users): a locked user's otherwise-valid Limen session cannot reach any route
//     under the mount except signout. This is the second, narrower layer resolveSession's own
//     locked check (above) can't provide on its own: that check only ever controls what
//     auth.FromContext returns for *this application's* handlers, because Limen's own plugin
//     routes (organization's invitations, Limen's own GET /me, an OAuth callback, ...)
//     authenticate against Limen's *own* session validation and never call FromContext at all.
//     Concretely: a locked user can still complete a fresh credential sign-in or an OAuth callback
//     — none of Limen's plugins know locked_users exists — minting a brand new, perfectly valid
//     Limen session; without this middleware they could then use that session against any Limen
//     route directly. The fresh session still gets minted (there is no hook early enough to stop
//     that part), but it is useless the moment it tries to do anything except sign out.
//  2. Verification (users.email_verified_at): an unverified session may only reach the routes in
//     authMountUnverifiedAllowed. Same two-layer reasoning — RequireSession covers our handlers,
//     this covers Limen's — and it is why an unverified user cannot, say, accept an invitation
//     addressed to an email they never proved they own.
//
// Fails closed like resolveSession: a locked_users query error is treated as "locked" rather than
// "couldn't tell, so let it through" — see migrations/00007_admin_locks.sql for the full picture.
func (s *Service) AuthMountGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleaned := path.Clean(strings.TrimSuffix(r.URL.Path, "/"))
		route := r.Method + " " + cleaned
		if route == authMountSignoutMethodAndPath {
			next.ServeHTTP(w, r)
			return
		}

		validated, err := s.limen.GetSession(r)
		if err != nil || validated == nil || validated.User == nil {
			// No valid Limen session at all — nothing for this middleware to restrict; whatever
			// happens next (a signin attempt, an anonymous 401 from Limen itself, ...) is
			// unaffected by locked_users or verification.
			next.ServeHTTP(w, r)
			return
		}

		locked, err := s.isUserLocked(r.Context(), validated.User.ID)
		if err != nil {
			s.logger.Error("auth: locked_users check failed on auth mount; blocking request", "user_id", fmt.Sprint(validated.User.ID), "error", err)
			writeErrorEnvelope(w, http.StatusForbidden, "forbidden", "account is locked")
			return
		}
		if locked {
			writeErrorEnvelope(w, http.StatusForbidden, "forbidden", "account is locked")
			return
		}

		if validated.User.EmailVerifiedAt == nil {
			if _, ok := authMountUnverifiedAllowed[route]; !ok {
				writeUnverified(w)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Turn auto-sign-in off (auth.go)**

In `buildLimenConfig`'s `credentialpassword.New(...)` call, add after `credentialpassword.WithPasswordRequireNumbers(false),`:

```go
			// A fresh signup must NOT get a session: the account is unusable until the mailed
			// verification token is consumed (see AuthMountGuard/RequireSession in session.go),
			// and handing out a cookie first would only let the SPA discover that one request
			// later. Signup responds with the user payload (through sessionTransformer, so the
			// after-hook in signup_hook.go still sees the auth result) and no Set-Cookie; the
			// user signs in explicitly afterwards.
			credentialpassword.WithAutoSignInOnSignUp(false),
```

- [ ] **Step 5: Rename the mount wrapper in httpserver/server.go**

In `routes()`, replace lines 99-103 (`// LockedSessionMiddleware sits directly …` through `lockedAuthHandler := …`) with:

```go
	// AuthMountGuard sits directly around Limen's own handler, inside the rate limit — see its
	// own doc comment (internal/auth/session.go) for why resolveSession's lock/verification checks
	// alone can't stop a locked or unverified user's fresh sign-in from reaching Limen's own routes
	// (invitations, /me, ...) directly.
	guardedAuthHandler := s.authSvc.AuthMountGuard(authHandler)
```

and replace the two later uses of `lockedAuthHandler` with `guardedAuthHandler`.

- [ ] **Step 6: Seed route — sign in after signup, mark verified (httpserver/testroutes.go)**

Replace the second bullet of the package doc comment (`//   - No manual "set email_verified" step: …` through `//     a fresh signup either.`) with:

```go
//   - The "set email_verified" step is authSvc.MarkEmailVerified: a fresh Limen signup is
//     unverified and mints no session (internal/auth.buildLimenConfig turns auto-sign-in off), and
//     every gated route refuses an unverified session (internal/auth/session.go's RequireSession
//     and AuthMountGuard) — so, like seed.ts before it, this route marks the user verified and then
//     signs them in itself to obtain the cookies its own seeding needs.
```

In `handleSeed`, replace

```go
		cookies, err := seedSignUp(authSvc, email, password)
		if err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signup failed: "+err.Error(), nil)
			return
		}
```

with

```go
		if err := seedSignUp(authSvc, email, password); err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signup failed: "+err.Error(), nil)
			return
		}
		if err := authSvc.MarkEmailVerified(r.Context(), email); err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: marking verified failed: "+err.Error(), nil)
			return
		}
		cookies, err := seedSignIn(authSvc, email, password)
		if err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signin failed: "+err.Error(), nil)
			return
		}
```

Replace the `seedSignUp` function and its doc comment (from `// seedSignUp drives Limen's own signup route in-process …` through the function's closing brace; keep `seedRemoteAddrCounter`/`nextSeedRemoteAddr` untouched) with:

```go
// seedSignUp drives Limen's own signup route in-process (authSvc.Handler(), the exact handler
// internal/httpserver.Server mounts at "/api/v1/auth/") via httptest, exactly the way a browser's
// POST to /api/v1/auth/signup/credential would, so the created user's password hash is whatever
// Limen itself produces — nothing here reaches into Limen's own tables directly. Signup mints no
// session (auto-sign-in is off — see internal/auth.buildLimenConfig), so this returns nothing
// but an error; seedSignIn below is what yields cookies.
func seedSignUp(authSvc *auth.Service, email, password string) error {
	_, err := seedAuthPost(authSvc, "/api/v1/auth/signup/credential", map[string]string{"email": email, "password": password})
	return err
}

// seedSignIn drives Limen's signin route the same way and returns the session cookies it set.
func seedSignIn(authSvc *auth.Service, email, password string) ([]*http.Cookie, error) {
	return seedAuthPost(authSvc, "/api/v1/auth/signin/credential", map[string]string{"credential": email, "password": password})
}

func seedAuthPost(authSvc *auth.Service, path string, body map[string]string) ([]*http.Cookie, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal %s body: %w", path, err)
	}

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = nextSeedRemoteAddr()
	rec := httptest.NewRecorder()
	authSvc.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("%s: status %d: %s", path, res.StatusCode, respBody)
	}
	return res.Cookies(), nil
}
```

- [ ] **Step 7: Update the existing tests that relied on signup minting a verified, usable session**

`internal/auth/auth_test.go` — add this helper after `decodeJSON`:

```go
// signUpVerifiedAndSignIn is the "give me a usable account" helper: signup (which mints no session
// since auto-sign-in is off), MarkEmailVerified (the gate refuses unverified sessions everywhere
// that matters), then signin on ts.client's cookie jar.
func (ts *testService) signUpVerifiedAndSignIn(t *testing.T, email string) {
	t.Helper()
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	if err := ts.svc.MarkEmailVerified(context.Background(), email); err != nil {
		t.Fatalf("MarkEmailVerified(%q): %v", email, err)
	}
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email,
		"password":   signupPassword,
	}), "signin")
}
```

Then replace every four-line `requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{ "email": <var>, "password": signupPassword, }), "signup")` block that is FOLLOWED by a request needing a usable session with `ts.signUpVerifiedAndSignIn(t, <var>)`:

- `auth_test.go`: `TestMeReflectsStaffFlagAfterMakeStaff` (line ~248), `TestStaffFlagAndRequireStaff` (~343), `TestRequireOrgMemberForbiddenForNonMember` (both the owner at ~457 and the outsider at ~478), `TestRequireOrgMemberInternalErrorOnDBFailure` (~506). Leave `TestSignupSigninMeFlow`, `TestPasswordResetEnqueuesMail`, `TestSessionCookieSecureMatchesAppURLScheme` and `TestOAuthRoutesAbsentWithoutConfig` as they are (they sign in explicitly or need no session).
- `personal_org_test.go`: the signup blocks at lines ~61, ~97, ~131, ~182, ~264, ~301 and the one inside the `for` loop in `TestPersonalOrgSlugCollisionAcrossDomainsBothSucceed` (~232) all become `ts.signUpVerifiedAndSignIn(t, email)`.
- `staff_cli_test.go`: unchanged (MakeStaff needs no session).

`internal/admin/users_test.go` — replace `signUpAndSignIn` (lines 481-485) with:

```go
func (h *authHarness) signUpAndSignIn(t *testing.T, email string) {
	t.Helper()
	h.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{"email": email, "password": harnessPassword})
	h.markVerified(t, email)
	h.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{"credential": email, "password": harnessPassword})
}

// markVerified flips email_verified_at through the real auth.Service — every RequireSession/
// RequireStaff route this harness probes refuses an unverified session (internal/auth/session.go).
func (h *authHarness) markVerified(t *testing.T, email string) {
	t.Helper()
	if err := h.svc.MarkEmailVerified(context.Background(), email); err != nil {
		t.Fatalf("MarkEmailVerified(%q): %v", email, err)
	}
}
```

`internal/admin/handlers_test.go` — in `staffClient`, insert `h.markVerified(t, email)` between the signup and signin `postJSONWith` lines.

- [ ] **Step 8: Run everything that touches auth**

Run: `go build ./... && go test ./internal/auth/ ./internal/admin/ ./internal/httpserver/`
Expected: all `ok`. (`TestLockedSessionMiddleware_BlocksFreshSignInAtLimenButAllowsSignout` still passes: the lock check runs before the verification check, so a locked user's `/me` is still `forbidden`. `TestSeed_*` pass because the seed route now marks verified and signs in.)

- [ ] **Step 9: Full suite, lint, commit**

Run: `go vet ./... && golangci-lint run ./... && go test ./...`
Expected: no findings, all `ok`.

```bash
git add internal/auth internal/httpserver internal/admin
git commit -m "fix(auth): enforce email verification; signup no longer mints a session

Unverified sessions get 403 email_unverified from RequireSession/RequireStaff
and from the Limen mount (AuthMountGuard, formerly LockedSessionMiddleware),
except /me, signout, verify-email, email-verifications and the session-less
credential routes. Pins the mailed-token round trip end to end.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 6: Account service — shared cascade, own-account deletion, password re-check, org list/switch

**Files:**
- Create: `internal/auth/cascade.go`, `internal/auth/account.go`, `internal/auth/account_test.go`
- Modify: `internal/auth/auth.go` (`Service.passwords` field, set in `newService`), `internal/admin/users.go:455-593` (`DeleteUser` delegates; `cascadeOrphanedOwnerOrganizations` removed)

**Interfaces:**
- Consumes: `credentialpassword.Use(*limen.Limen).ComparePassword(password string, hash *string) (bool, error)`; `organization.API.SwitchOrganization`/`CheckMemberExistsInOrganization`.
- Produces:
  ```go
  func CascadeDeleteUser(ctx context.Context, tx *sql.Tx, userID string) error // ErrNoSuchUser when no row
  var ErrPasswordRequired = errors.New("auth: current password required")
  var ErrPasswordMismatch = errors.New("auth: current password does not match")
  func (s *Service) CheckOwnPassword(ctx context.Context, userID, password string) error
  func (s *Service) DeleteOwnAccount(ctx context.Context, userID string) error
  type OrgSummary struct { ID, Name, Slug string; Active bool }
  func (s *Service) ListUserOrganizations(ctx context.Context, sess *Session) ([]OrgSummary, error)
  func (s *Service) SwitchOrganization(w http.ResponseWriter, r *http.Request, orgID string) error // ErrUnauthenticated / ErrForbidden / ErrInternal
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/auth/account_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"testing"
)

func countRows(t *testing.T, ts *testService, query string, args ...any) int {
	t.Helper()
	var n int
	if err := ts.svc.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestCheckOwnPassword(t *testing.T) {
	ts := newTestService(t)
	email := "pw-check@example.com"
	userID := signUp(t, ts, email)
	ctx := context.Background()

	if err := ts.svc.CheckOwnPassword(ctx, userID, signupPassword); err != nil {
		t.Errorf("correct password: %v, want nil", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, userID, "definitely-not-it"); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("wrong password: %v, want ErrPasswordMismatch", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, userID, ""); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("empty password: %v, want ErrPasswordRequired", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, "999999", signupPassword); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("unknown user: %v, want ErrNoSuchUser", err)
	}

	// An OAuth-only account has no credential to re-check; deletion must not demand one.
	if _, err := ts.svc.db.ExecContext(ctx, "UPDATE users SET password = NULL WHERE id = $1", lookupUserID(t, ts, email)); err != nil {
		t.Fatalf("nulling password: %v", err)
	}
	if err := ts.svc.CheckOwnPassword(ctx, userID, ""); err != nil {
		t.Errorf("oauth-only account, empty password: %v, want nil", err)
	}
}

func TestDeleteOwnAccountCascades(t *testing.T) {
	ts := newTestService(t)
	email := "delete-me@example.com"
	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts) // creates the personal org
	userID := fmt.Sprint(lookupUserID(t, ts, email))
	ctx := context.Background()

	if got := countRows(t, ts, "SELECT count(*) FROM organization_members WHERE user_id = $1", lookupUserID(t, ts, email)); got != 1 {
		t.Fatalf("memberships before delete = %d, want 1", got)
	}
	nb := "nb"
	if err := ts.svc.SetProfile(ctx, userID, nil, &nb); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}

	if err := ts.svc.DeleteOwnAccount(ctx, userID); err != nil {
		t.Fatalf("DeleteOwnAccount: %v", err)
	}

	if got := countRows(t, ts, "SELECT count(*) FROM users WHERE email = $1", email); got != 0 {
		t.Errorf("users rows after delete = %d, want 0", got)
	}
	if got := countRows(t, ts, "SELECT count(*) FROM organizations WHERE slug LIKE 'delete-me-%'"); got != 0 {
		t.Errorf("sole-owned personal org survived deletion (%d rows)", got)
	}
	if got := countRows(t, ts, "SELECT count(*) FROM user_preferences"); got != 0 {
		t.Errorf("user_preferences rows after delete = %d, want 0 (FK cascade)", got)
	}
	// The cookie the client still holds is dead.
	probe := decodeJSON(t, ts.get(t, "/probe"))
	if anon, _ := probe["anonymous"].(bool); !anon {
		t.Errorf("probe still sees a session after account deletion: %+v", probe)
	}
	if err := ts.svc.DeleteOwnAccount(ctx, userID); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("second DeleteOwnAccount = %v, want ErrNoSuchUser", err)
	}
}

func TestListAndSwitchOrganizations(t *testing.T) {
	ts := newTestService(t)
	email := "switcher@example.com"
	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)
	ctx := context.Background()

	// A second org through Limen's own route (the SPA has no other way to create one).
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/", map[string]any{
		"name": "Team Ada", "slug": "team-ada",
	}), "create org")

	sess := &Session{UserID: fmt.Sprint(lookupUserID(t, ts, email))}
	probe := decodeJSON(t, ts.get(t, "/probe"))
	sess.ActiveOrgID, _ = probe["ActiveOrgID"].(string)

	orgs, err := ts.svc.ListUserOrganizations(ctx, sess)
	if err != nil {
		t.Fatalf("ListUserOrganizations: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("len(orgs) = %d, want 2: %+v", len(orgs), orgs)
	}
	var team, personal *OrgSummary
	for i := range orgs {
		switch orgs[i].Slug {
		case "team-ada":
			team = &orgs[i]
		default:
			personal = &orgs[i]
		}
	}
	if team == nil || personal == nil {
		t.Fatalf("expected a personal org and team-ada, got %+v", orgs)
	}
	if !personal.Active || team.Active {
		t.Errorf("Active flags: personal=%v team=%v, want true/false", personal.Active, team.Active)
	}
	if team.ID == "" || team.Name != "Team Ada" {
		t.Errorf("team summary = %+v", *team)
	}

	// Switch through the probe route (SwitchOrganization needs the request's Limen session).
	requireStatus2xx(t, ts.get(t, "/probe/switch?org="+team.ID), "switch to team")
	probe = decodeJSON(t, ts.get(t, "/probe"))
	if got, _ := probe["ActiveOrgID"].(string); got != team.ID {
		t.Errorf("ActiveOrgID after switch = %q, want %q", got, team.ID)
	}

	// Someone else's org: forbidden, and the active org does not move.
	outsider := "outsider-switch@example.com"
	fresh := &testService{svc: ts.svc, mail: ts.mail, server: ts.server}
	jar, _ := cookiejar.New(nil)
	fresh.client = &http.Client{Jar: jar}
	fresh.signUpVerifiedAndSignIn(t, outsider)
	resp := fresh.get(t, "/probe/switch?org="+team.ID)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("outsider switch status = %d, want 403", resp.StatusCode)
	}
	resp2 := fresh.get(t, "/probe/switch?org=not-a-number")
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("garbage org id status = %d, want 403", resp2.StatusCode)
	}
}
```

Add the switch probe to `newTestServiceWithConfig` in `auth_test.go` (after the `/probe/session-unverified-ok` route):

```go
	mux.HandleFunc("/probe/switch", svc.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		switch err := svc.SwitchOrganization(w, r, r.URL.Query().Get("org")); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrForbidden):
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'TestCheckOwnPassword|TestDeleteOwnAccountCascades|TestListAndSwitchOrganizations'`
Expected: FAIL to compile — `CheckOwnPassword`, `DeleteOwnAccount`, `ListUserOrganizations`, `SwitchOrganization`, `OrgSummary` undefined.

- [ ] **Step 3: Move the cascade into internal/auth**

Create `internal/auth/cascade.go`:

```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// CascadeDeleteUser removes userID and everything that must go with them, inside the caller's
// transaction. Shared by internal/admin.DeleteUser (staff-initiated) and Service.DeleteOwnAccount
// (self-service) so the two can never drift. Ports user-delete.workers.test.ts's semantics
// against Limen's organization/member schema: every organization where userID is the sole
// `owner`-role member is handed to another member (promoting the oldest remaining one, by
// membership created_at) or, if userID was the org's last member of any role, deleted outright,
// taking its polls/booking pages with it via their own ON DELETE CASCADE from organizations. An
// org with another owner survives untouched, its content untouched, and userID's own
// `created_by`/`member_user_id` references in it simply go null via their own ON DELETE SET NULL.
//
// Returns ErrNoSuchUser (after the cascade ran to no effect) when no users row matched. The
// caller commits; the caller also revokes Limen sessions best-effort beforehand (see
// Service.RevokeUserSessions) — the DELETE FROM sessions here is the hard FK enforcement.
func CascadeDeleteUser(ctx context.Context, tx *sql.Tx, userID string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrNoSuchUser
	}

	if err := cascadeOrphanedOwnerOrganizations(ctx, tx, uid); err != nil {
		return fmt.Errorf("auth: cascading organizations owned by user %s: %w", userID, err)
	}

	// accounts/sessions/two_factors all reference users(id) ON DELETE RESTRICT (migrations/00002),
	// unlike organization_members and user_preferences (CASCADE, which the DELETE FROM users
	// below triggers on its own) — so they must be cleared explicitly first, or that statement
	// fails a foreign key check. Plan B's 00010_drop_two_factor.sql removes the two_factors line
	// along with the table.
	for _, stmt := range []string{
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM accounts WHERE user_id = $1`,
		`DELETE FROM two_factors WHERE user_id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, uid); err != nil {
			return fmt.Errorf("auth: clearing dependent rows for user %s: %w", userID, err)
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, uid)
	if err != nil {
		return fmt.Errorf("auth: deleting user %s: %w", userID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchUser
	}
	return nil
}

// cascadeOrphanedOwnerOrganizations is deleteOrphanedOwnerOrganizations (personal-org.ts) ported
// against Limen's schema: an organization's owners are its organization_members rows that have an
// 'owner'-named row in organization_member_roles, rather than a single `member.role` column.
func cascadeOrphanedOwnerOrganizations(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.organization_id
		FROM organization_members m
		JOIN organization_member_roles mr ON mr.member_id = m.id AND mr.role = 'owner'
		WHERE m.user_id = $1
	`, userID)
	if err != nil {
		return err
	}
	var ownedOrgIDs []int64
	for rows.Next() {
		var orgID int64
		if err := rows.Scan(&orgID); err != nil {
			_ = rows.Close()
			return err
		}
		ownedOrgIDs = append(ownedOrgIDs, orgID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	for _, orgID := range ownedOrgIDs {
		var otherOwnerExists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM organization_members m2
				JOIN organization_member_roles mr2 ON mr2.member_id = m2.id AND mr2.role = 'owner'
				WHERE m2.organization_id = $1 AND m2.user_id <> $2
			)
		`, orgID, userID).Scan(&otherOwnerExists); err != nil {
			return err
		}
		if otherOwnerExists {
			continue
		}

		var oldestOtherMemberID sql.NullInt64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM organization_members
			WHERE organization_id = $1 AND user_id <> $2
			ORDER BY created_at ASC LIMIT 1
		`, orgID, userID).Scan(&oldestOtherMemberID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		if oldestOtherMemberID.Valid {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO organization_member_roles (organization_id, member_id, role)
				VALUES ($1, $2, 'owner')
				ON CONFLICT (member_id, role) DO NOTHING
			`, orgID, oldestOtherMemberID.Int64); err != nil {
				return err
			}
			continue
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM organizations WHERE id = $1`, orgID); err != nil {
			return err
		}
	}
	return nil
}
```

In `internal/admin/users.go`, replace the body of `DeleteUser` from `uid, err := strconv.ParseInt(userID, 10, 64)` down to (and including) the `if n, _ := res.RowsAffected(); n == 0 {…}` block with:

```go
	if _, err := strconv.ParseInt(userID, 10, 64); err != nil {
		return fmt.Errorf("admin: invalid user id %q: %w", userID, err)
	}

	// Best-effort, before the tx below: reaches whatever Limen-side session bookkeeping lives
	// beyond the raw `sessions` table (see RevokeUserSessions' own doc comment). Not required for
	// correctness — CascadeDeleteUser's own DELETE FROM sessions is what actually satisfies
	// sessions.user_id's ON DELETE RESTRICT — so a failure here is not fatal to the delete.
	if authSvc != nil {
		_ = authSvc.RevokeUserSessions(ctx, userID)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("admin: begin delete-user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The cascade itself (orphaned-owner orgs, dependent rows, the users row) is shared with the
	// self-service path — internal/auth.CascadeDeleteUser — so staff deletion and "delete my
	// account" can never disagree about what deleting a user means.
	if err := auth.CascadeDeleteUser(ctx, tx, userID); err != nil {
		if errors.Is(err, auth.ErrNoSuchUser) {
			return fmt.Errorf("admin: no user with id %q", userID)
		}
		return err
	}
```

(keep the `Record(ctx, tx, actor, ActionDeleteUser, …)` call and `return tx.Commit()` that follow), delete the now-unused `cascadeOrphanedOwnerOrganizations` function (users.go:524-593) and its doc comment, and update `DeleteUser`'s doc comment to end with "The cascade lives in internal/auth.CascadeDeleteUser; see its doc comment for the exact semantics." Remove any import that becomes unused (`database/sql` stays — `*sql.DB` is still a parameter).

- [ ] **Step 4: Account service methods**

Add to the `Service` struct in `auth.go` (after `orgs organization.API`):

```go
	passwords   credentialpassword.API // ComparePassword for the delete-account re-check
```

and in `newService`, after `s.orgs = organization.Use(a)`:

```go
	s.passwords = credentialpassword.Use(a)
```

Create `internal/auth/account.go`:

```go
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/thecodearcher/limen/plugins/organization"
)

// ErrPasswordRequired: the account has a password and the caller supplied none.
var ErrPasswordRequired = errors.New("auth: current password required")

// ErrPasswordMismatch: the supplied current password does not verify.
var ErrPasswordMismatch = errors.New("auth: current password does not match")

// CheckOwnPassword re-verifies a signed-in user's current password before a destructive action
// (DELETE /api/v1/me). An account with no password at all (OAuth-only) passes with any input — a
// credential that does not exist cannot be re-entered. Verification uses Limen's own Argon2id
// hasher through credential-password's exported API (ComparePassword), never a re-implementation.
func (s *Service) CheckOwnPassword(ctx context.Context, userID, password string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrNoSuchUser
	}
	var hash sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT password FROM users WHERE id = $1`, uid).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchUser
	}
	if err != nil {
		return fmt.Errorf("auth: loading password hash: %w", err)
	}
	if !hash.Valid || hash.String == "" {
		return nil
	}
	if password == "" {
		return ErrPasswordRequired
	}
	ok, err := s.passwords.ComparePassword(password, &hash.String)
	if err != nil {
		return fmt.Errorf("auth: comparing password: %w", err)
	}
	if !ok {
		return ErrPasswordMismatch
	}
	return nil
}

// DeleteOwnAccount deletes the caller's own account with exactly admin.DeleteUser's cascade
// (CascadeDeleteUser) — sole-owned organizations and their content go, co-owned ones survive. The
// HTTP layer is responsible for CheckOwnPassword first. Limen sessions are revoked best-effort
// before the transaction (same as the admin path); the transaction's own DELETE FROM sessions is
// the hard guarantee. The in-process personal-org cache is cleared so a re-registration under the
// same id (impossible with BIGSERIAL, but cheap to be correct about) is never skipped.
func (s *Service) DeleteOwnAccount(ctx context.Context, userID string) error {
	_ = s.RevokeUserSessions(ctx, userID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin delete-account tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := CascadeDeleteUser(ctx, tx, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit delete-account tx: %w", err)
	}
	s.personalOrgEnsured.Delete(userID)
	return nil
}

// OrgSummary is one row of ListUserOrganizations — what the SPA's org switcher renders. ID is the
// stringified organization id (the seam's convention), because Limen's own GET /organizations/
// serializes organizations WITHOUT an id (SerializeModel drops non-string ids), which is why the
// switcher cannot be built on Limen's routes alone.
type OrgSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Slug   string `json:"slug"`
	Active bool   `json:"active"`
}

// ListUserOrganizations lists every organization sess's user belongs to, by name, flagging the
// session's active one.
func (s *Service) ListUserOrganizations(ctx context.Context, sess *Session) ([]OrgSummary, error) {
	uid, err := strconv.ParseInt(sess.UserID, 10, 64)
	if err != nil {
		return nil, ErrNoSuchUser
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.name, o.slug
		FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY o.name, o.id
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("auth: listing organizations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]OrgSummary, 0)
	for rows.Next() {
		var (
			id         int64
			name, slug string
		)
		if err := rows.Scan(&id, &name, &slug); err != nil {
			return nil, fmt.Errorf("auth: scanning organization: %w", err)
		}
		idStr := strconv.FormatInt(id, 10)
		out = append(out, OrgSummary{ID: idStr, Name: name, Slug: slug, Active: idStr == sess.ActiveOrgID})
	}
	return out, rows.Err()
}

// SwitchOrganization makes orgID the active organization of the Limen session carried by r,
// after verifying membership. Takes w/r rather than a ctx because it needs Limen's own session
// (GetSession) and must forward any re-issued session cookie. Errors: ErrUnauthenticated (no
// Limen session), ErrForbidden (not a member, unknown or malformed org id), ErrInternal (wrapped)
// otherwise.
func (s *Service) SwitchOrganization(w http.ResponseWriter, r *http.Request, orgID string) error {
	validated, err := s.limen.GetSession(r)
	if err != nil || validated == nil || validated.User == nil || validated.Session == nil {
		return ErrUnauthenticated
	}
	id, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return ErrForbidden
	}
	if err := s.orgs.CheckMemberExistsInOrganization(r.Context(), id, validated.User.ID); err != nil {
		if errors.Is(err, organization.ErrMemberNotInOrganization) {
			return ErrForbidden
		}
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	_, result, err := s.orgs.SwitchOrganization(r.Context(), validated.Session, id)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}
	if result != nil && result.Cookie != nil {
		http.SetCookie(w, result.Cookie)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test ./internal/auth/ ./internal/admin/`
Expected: all `ok` (admin's `TestDeleteUser_*` cases still pass — the cascade code moved verbatim).

- [ ] **Step 6: Lint and commit**

Run: `go vet ./... && golangci-lint run ./...`
Expected: no findings.

```bash
git add internal/auth/cascade.go internal/auth/account.go internal/auth/account_test.go internal/auth/auth.go internal/auth/auth_test.go internal/admin/users.go
git commit -m "feat(auth): self-service account deletion, password re-check, org list/switch

CascadeDeleteUser moves from internal/admin into the seam so staff deletion and
DELETE /api/v1/me share one definition of deleting a user.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 7: HTTP account routes — `PATCH/DELETE /api/v1/me`, `GET /api/v1/me/organizations`, `POST /api/v1/me/active-organization`

**Files:**
- Create: `internal/httpserver/account.go`, `internal/httpserver/account_test.go`
- Modify: `internal/httpserver/server.go` (`routes()`), `internal/auth/routes.txt` (append section)

**Interfaces:**
- Consumes: `auth.Service.{SetProfile, CheckOwnPassword, DeleteOwnAccount, ListUserOrganizations, SwitchOrganization, RequireSession, RequireSessionAllowUnverified}`, `auth.{ProfileValidationError, ErrNoSuchUser, ErrPasswordRequired, ErrPasswordMismatch, ErrForbidden, ErrUnauthenticated}`.
- Produces (the SPA codes against these):
  - `PATCH /api/v1/me` `{ "name"?: string, "locale"?: string }` → 204; 422 `invalid` with `fields.name`/`fields.locale`; 400 `invalid` when both absent. Works for unverified sessions.
  - `DELETE /api/v1/me` `{ "password"?: string }` (body optional) → 204; 400 `password_required`; 403 `invalid_password`. Works for unverified sessions.
  - `GET /api/v1/me/organizations` → `[{id, name, slug, active}]` (verified only).
  - `POST /api/v1/me/active-organization` `{ "orgId": string }` → 204; 403 `forbidden` (verified only).

- [ ] **Step 1: Write the failing tests**

Create `internal/httpserver/account_test.go`:

```go
package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// accountHarness runs the real Server.Handler() (every middleware, the auth mount, the account
// routes) behind httptest, with one cookie-jar client.
type accountHarness struct {
	t       *testing.T
	authSvc *auth.Service
	server  *httptest.Server
	client  *http.Client
}

func newAccountHarness(t *testing.T) *accountHarness {
	t.Helper()
	d := testdb.New(t)
	cfg := testConfig()
	authSvc := testAuthService(t, cfg, d)
	srv := httpserver.New(cfg, d, authSvc)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &accountHarness{t: t, authSvc: authSvc, server: ts, client: &http.Client{Jar: jar}}
}

func (h *accountHarness) do(method, path string, body any) *http.Response {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func (h *accountHarness) mustStatus(resp *http.Response, want int, what string) map[string]any {
	h.t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		h.t.Fatalf("%s: status %d, want %d: %s", what, resp.StatusCode, want, raw)
	}
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// must2xx is mustStatus for routes whose exact success code is Limen's business (200 vs 201).
func (h *accountHarness) must2xx(resp *http.Response, what string) {
	h.t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("%s: status %d, want 2xx: %s", what, resp.StatusCode, raw)
	}
}

func (h *accountHarness) errorCode(body map[string]any) string {
	errObj, _ := body["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

const accountPassword = "Str0ngPassw0rd"

func (h *accountHarness) signUp(email string) {
	h.t.Helper()
	h.must2xx(h.do(http.MethodPost, "/api/v1/auth/signup/credential", map[string]any{"email": email, "password": accountPassword}), "signup")
}

func (h *accountHarness) signIn(email string) {
	h.t.Helper()
	h.must2xx(h.do(http.MethodPost, "/api/v1/auth/signin/credential", map[string]any{"credential": email, "password": accountPassword}), "signin")
}

func (h *accountHarness) verify(email string) {
	h.t.Helper()
	if err := h.authSvc.MarkEmailVerified(context.Background(), email); err != nil {
		h.t.Fatalf("MarkEmailVerified: %v", err)
	}
}

func (h *accountHarness) me() map[string]any {
	h.t.Helper()
	body := h.mustStatus(h.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusOK, "GET /me")
	user, _ := body["user"].(map[string]any)
	return user
}

func TestPatchMe_UpdatesNameAndLocaleEvenBeforeVerification(t *testing.T) {
	h := newAccountHarness(t)
	email := "patch-me@example.com"
	h.signUp(email)
	h.signIn(email) // unverified on purpose

	h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{"name": "Ada Lovelace", "locale": "nb"}), http.StatusNoContent, "PATCH /me")

	user := h.me()
	if got, _ := user["name"].(string); got != "Ada Lovelace" {
		t.Errorf("name = %q, want Ada Lovelace", got)
	}
	if got, _ := user["locale"].(string); got != "nb" {
		t.Errorf("locale = %q, want nb", got)
	}

	body := h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{"locale": "de"}), http.StatusUnprocessableEntity, "PATCH bad locale")
	if h.errorCode(body) != "invalid" {
		t.Errorf("error.code = %q, want invalid", h.errorCode(body))
	}
	fields, _ := body["error"].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields["locale"]; !ok {
		t.Errorf("fields = %+v, want a locale entry", fields)
	}

	body = h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{}), http.StatusBadRequest, "PATCH empty")
	if h.errorCode(body) != "invalid" {
		t.Errorf("error.code = %q, want invalid", h.errorCode(body))
	}
}

func TestPatchMe_RequiresASession(t *testing.T) {
	h := newAccountHarness(t)
	body := h.mustStatus(h.do(http.MethodPatch, "/api/v1/me", map[string]any{"name": "x"}), http.StatusUnauthorized, "anonymous PATCH")
	if h.errorCode(body) != "unauthenticated" {
		t.Errorf("error.code = %q, want unauthenticated", h.errorCode(body))
	}
}

func TestDeleteMe_RequiresCurrentPasswordForCredentialAccounts(t *testing.T) {
	h := newAccountHarness(t)
	email := "delete-me-http@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)

	body := h.mustStatus(h.do(http.MethodDelete, "/api/v1/me", nil), http.StatusBadRequest, "DELETE without body")
	if h.errorCode(body) != "password_required" {
		t.Errorf("error.code = %q, want password_required", h.errorCode(body))
	}
	body = h.mustStatus(h.do(http.MethodDelete, "/api/v1/me", map[string]any{"password": "wrong"}), http.StatusForbidden, "DELETE wrong password")
	if h.errorCode(body) != "invalid_password" {
		t.Errorf("error.code = %q, want invalid_password", h.errorCode(body))
	}
	h.me() // still alive

	h.mustStatus(h.do(http.MethodDelete, "/api/v1/me", map[string]any{"password": accountPassword}), http.StatusNoContent, "DELETE /me")
	h.mustStatus(h.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusUnauthorized, "GET /me after delete")
}

func TestMyOrganizations_ListAndSwitch(t *testing.T) {
	h := newAccountHarness(t)
	email := "orgs-http@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)
	h.me() // resolves the session once so the personal org exists and is active

	h.must2xx(h.do(http.MethodPost, "/api/v1/auth/organizations/", map[string]any{"name": "Team HTTP", "slug": "team-http"}), "create org")

	resp := h.do(http.MethodGet, "/api/v1/me/organizations", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me/organizations: %d", resp.StatusCode)
	}
	var orgs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&orgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("len(orgs) = %d, want 2: %+v", len(orgs), orgs)
	}
	var teamID string
	for _, o := range orgs {
		if o["slug"] == "team-http" {
			teamID, _ = o["id"].(string)
			if active, _ := o["active"].(bool); active {
				t.Error("team-http reported active before switching")
			}
		}
	}
	if teamID == "" {
		t.Fatalf("team-http has no id: %+v", orgs)
	}

	h.mustStatus(h.do(http.MethodPost, "/api/v1/me/active-organization", map[string]any{"orgId": teamID}), http.StatusNoContent, "switch")

	resp2 := h.do(http.MethodGet, "/api/v1/me/organizations", nil)
	defer func() { _ = resp2.Body.Close() }()
	var after []map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, o := range after {
		active, _ := o["active"].(bool)
		if (o["slug"] == "team-http") != active {
			t.Errorf("after switch: %v active=%v", o["slug"], active)
		}
	}

	body := h.mustStatus(h.do(http.MethodPost, "/api/v1/me/active-organization", map[string]any{"orgId": "999999"}), http.StatusForbidden, "switch to unknown org")
	if h.errorCode(body) != "forbidden" {
		t.Errorf("error.code = %q, want forbidden", h.errorCode(body))
	}
}

func TestMyOrganizations_GatedOnVerification(t *testing.T) {
	h := newAccountHarness(t)
	email := "orgs-unverified@example.com"
	h.signUp(email)
	h.signIn(email)
	body := h.mustStatus(h.do(http.MethodGet, "/api/v1/me/organizations", nil), http.StatusForbidden, "unverified list")
	if h.errorCode(body) != "email_unverified" {
		t.Errorf("error.code = %q, want email_unverified", h.errorCode(body))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/httpserver/ -run 'TestPatchMe|TestDeleteMe|TestMyOrganizations'`
Expected: FAIL — `PATCH /me: status 404, want 204` (the `/api/` catch-all).

- [ ] **Step 3: Implement the handlers**

Create `internal/httpserver/account.go`:

```go
package httpserver

// The signed-in user's own account: profile edits, deletion, and the organization switcher. Thin
// handlers over auth.Service — the seam owns validation, cascades and Limen access; this file only
// maps HTTP to those methods and their errors to the standard envelope. Mounted here (not in
// internal/auth) so the handlers can use this package's JSON/Err/DecodeJSON helpers; internal/auth
// must not import internal/httpserver.

import (
	"errors"
	"net/http"

	"github.com/refsdal/whenweall/internal/auth"
)

type updateMeRequest struct {
	Name   *string `json:"name"`
	Locale *string `json:"locale"`
}

type deleteMeRequest struct {
	Password string `json:"password"`
}

type switchOrganizationRequest struct {
	OrgID string `json:"orgId"`
}

// registerAccountRoutes mounts the /api/v1/me* routes. PATCH and DELETE deliberately use
// RequireSessionAllowUnverified: an unverified user must be able to set their locale (so the
// verification mail we resend is in their language) and to delete an account they cannot
// otherwise use. The organization routes are verified-only like everything else.
func (s *Server) registerAccountRoutes() {
	s.mux.HandleFunc("PATCH /api/v1/me", s.authSvc.RequireSessionAllowUnverified(s.handleUpdateMe))
	s.mux.HandleFunc("DELETE /api/v1/me", s.authSvc.RequireSessionAllowUnverified(s.handleDeleteMe))
	s.mux.HandleFunc("GET /api/v1/me/organizations", s.authSvc.RequireSession(s.handleListMyOrganizations))
	s.mux.HandleFunc("POST /api/v1/me/active-organization", s.authSvc.RequireSession(s.handleSwitchOrganization))
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	var req updateMeRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.Locale == nil {
		Err(w, http.StatusBadRequest, "invalid", "nothing to update: send name and/or locale", nil)
		return
	}
	if err := s.authSvc.SetProfile(r.Context(), sess.UserID, req.Name, req.Locale); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	var req deleteMeRequest
	// The body is optional (an OAuth-only account has no password to send), so only decode when
	// one was actually sent — DecodeJSON's "request body is required" 400 would be wrong here.
	if r.ContentLength != 0 {
		if !DecodeJSON(w, r, &req) {
			return
		}
	}
	if err := s.authSvc.CheckOwnPassword(r.Context(), sess.UserID, req.Password); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	if err := s.authSvc.DeleteOwnAccount(r.Context(), sess.UserID); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListMyOrganizations(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	orgs, err := s.authSvc.ListUserOrganizations(r.Context(), sess)
	if err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	JSON(w, http.StatusOK, orgs)
}

func (s *Server) handleSwitchOrganization(w http.ResponseWriter, r *http.Request) {
	var req switchOrganizationRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if err := s.authSvc.SwitchOrganization(w, r, req.OrgID); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mapAccountError maps the auth seam's account errors to the envelope (see WriteDomainError).
func mapAccountError(err error) (status int, code, message string, fields map[string]string, ok bool) {
	var verr *auth.ProfileValidationError
	switch {
	case errors.As(err, &verr):
		return http.StatusUnprocessableEntity, "invalid", "validation failed", map[string]string{verr.Field: verr.Message}, true
	case errors.Is(err, auth.ErrNoSuchUser):
		return http.StatusNotFound, "not_found", "user not found", nil, true
	case errors.Is(err, auth.ErrPasswordRequired):
		return http.StatusBadRequest, "password_required", "your current password is required", nil, true
	case errors.Is(err, auth.ErrPasswordMismatch):
		return http.StatusForbidden, "invalid_password", "your current password is incorrect", nil, true
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "forbidden", "you are not a member of that organization", nil, true
	case errors.Is(err, auth.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated", "authentication required", nil, true
	}
	return 0, "", "", nil, false
}
```

In `server.go`'s `routes()`, add `s.registerAccountRoutes()` immediately before the `// /api/ misses land here …` comment.

Append to `internal/auth/routes.txt`:

```
Application account routes (internal/httpserver/account.go — ours, our envelope, not Limen's)
--------------------------------------------------------------------------------------------
PATCH  /api/v1/me                          session (verified or not)   {name?, locale?} -> 204
DELETE /api/v1/me                          session (verified or not)   {password?} -> 204; 400 password_required, 403 invalid_password
GET    /api/v1/me/organizations            session (verified)          -> [{id, name, slug, active}]
POST   /api/v1/me/active-organization      session (verified)          {orgId} -> 204; 403 forbidden

These exist because Limen's GET /organizations/ serializes organizations WITHOUT an id
(SerializeModel drops non-string ids), so the SPA cannot drive POST /organizations/switch itself.
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/httpserver/`
Expected: `ok`.

- [ ] **Step 5: Lint and commit**

Run: `go vet ./... && golangci-lint run ./...`
Expected: no findings.

```bash
git add internal/httpserver/account.go internal/httpserver/account_test.go internal/httpserver/server.go internal/auth/routes.txt
git commit -m "feat(http): /api/v1/me profile, deletion and organization switch routes

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 8: `internal/clientip` + Limen's rate limiter keyed like ours (and off for `/me`)

**Files:**
- Create: `internal/clientip/clientip.go`, `internal/clientip/clientip_test.go`
- Modify: `internal/httpserver/ratelimit.go` (`ClientIP` delegates), `internal/auth/auth.go` (`httpConfigOptions`)
- Test: `internal/auth/ratelimit_test.go` (new)

**Interfaces:**
- Produces: `clientip.FromRequest(r *http.Request, trustProxy bool) string` (identical semantics to `httpserver.ClientIP`, which becomes a one-line wrapper and keeps its name for existing callers). Limen's built-in limiter keys on the same value and no longer limits `GET /api/v1/auth/me` or `GET /api/v1/auth/organizations/active`.

- [ ] **Step 1: Write the failing tests**

Create `internal/clientip/clientip_test.go`:

```go
package clientip

import (
	"net/http"
	"testing"
)

func TestFromRequest(t *testing.T) {
	cases := []struct {
		name       string
		remote     string
		xff        string
		trustProxy bool
		want       string
	}{
		{"remote addr host:port", "203.0.113.9:4444", "", false, "203.0.113.9"},
		{"ignores XFF without trust", "203.0.113.9:4444", "198.51.100.1", false, "203.0.113.9"},
		{"rightmost XFF with trust", "10.0.0.2:4444", "198.51.100.1, 198.51.100.2", true, "198.51.100.2"},
		{"single XFF with trust", "10.0.0.2:4444", "198.51.100.1", true, "198.51.100.1"},
		{"blank XFF with trust falls back", "10.0.0.2:4444", " , ", true, "10.0.0.2"},
		{"remote addr without port", "203.0.113.9", "", false, "203.0.113.9"},
		{"ipv6 remote addr", "[2001:db8::1]:443", "", false, "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := FromRequest(r, tc.trustProxy); got != tc.want {
				t.Errorf("FromRequest = %q, want %q", got, tc.want)
			}
		})
	}
}
```

Create `internal/auth/ratelimit_test.go`:

```go
package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestLimenRateLimiterIgnoresSpoofedForwardedFor: with TrustProxy off (the test config's zero
// value), six failed sign-ins from one RemoteAddr must trip credential-password's 5-per-10s rule
// even though every request claims a different X-Forwarded-For. Before this task Limen keyed on
// the raw header, so the six requests would have landed in six separate buckets.
func TestLimenRateLimiterIgnoresSpoofedForwardedFor(t *testing.T) {
	ts := newTestService(t)
	email := "spoof@example.com"
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": signupPassword,
	}), "signup")

	var sawTooMany bool
	for i := 0; i < 6; i++ {
		body, _ := json.Marshal(map[string]any{"credential": email, "password": "wrong-password"})
		req, err := http.NewRequest(http.MethodPost, ts.url("/api/v1/auth/signin/credential"), strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		resp, err := ts.client.Do(req)
		if err != nil {
			t.Fatalf("signin %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			sawTooMany = true
		}
	}
	if !sawTooMany {
		t.Fatal("six failed sign-ins with distinct X-Forwarded-For never hit 429 — Limen's limiter is keying on the spoofable header")
	}
}

// TestMeIsNotRateLimitedByLimen: the SPA reads /me on every navigation; Limen's default 100/min
// global rule used to cover it. 105 reads in a row must all succeed.
func TestMeIsNotRateLimitedByLimen(t *testing.T) {
	ts := newTestService(t)
	ts.signUpVerifiedAndSignIn(t, "busy@example.com")
	for i := 0; i < 105; i++ {
		resp := ts.get(t, "/api/v1/auth/me")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /me #%d: status %d, want 200", i+1, resp.StatusCode)
		}
	}
	for i := 0; i < 105; i++ {
		resp := ts.get(t, "/api/v1/auth/organizations/active")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /organizations/active #%d: status %d, want 200", i+1, resp.StatusCode)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/clientip/ ./internal/auth/ -run 'TestFromRequest|TestLimenRateLimiter|TestMeIsNotRateLimited'`
Expected: clientip fails to build (`no Go files` / undefined); `TestLimenRateLimiterIgnoresSpoofedForwardedFor` FAILs with "never hit 429"; `TestMeIsNotRateLimitedByLimen` FAILs around request #101 with 429.

- [ ] **Step 3: Implement**

Create `internal/clientip/clientip.go`:

```go
// Package clientip derives the client address a request should be attributed to, honouring the
// app's TRUST_PROXY setting. It is its own package (rather than living in internal/httpserver,
// where ClientIP was born) so internal/auth can key Limen's built-in rate limiter on the exact
// same value without importing internal/httpserver — which imports internal/auth, so that edge
// would be a cycle.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// FromRequest returns the request's client IP: the rightmost entry of X-Forwarded-For when
// trustProxy is true (set from the app's TRUST_PROXY config — true only when a reverse proxy in
// front of us is trusted to set that header honestly), otherwise the host portion of RemoteAddr.
//
// Rightmost, not leftmost: X-Forwarded-For is a client-supplied header up until the first proxy
// that actually terminates the request touches it, and every hop after that only ever *appends*
// its own observed peer address to the end of the list — it never rewrites what's already there.
// So the leftmost entry is whatever the original client claimed for itself (trivially spoofed by
// sending an X-Forwarded-For header of their own choosing), while the rightmost entry is the
// address our own trusted proxy saw the connection come from — the only entry in the list this
// process didn't just take the client's word for.
func FromRequest(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if last != "" {
				return last
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
```

In `internal/httpserver/ratelimit.go`, replace the `ClientIP` function (and its doc comment) with:

```go
// ClientIP is clientip.FromRequest under its historical name — every rate limiter and captcha
// check in this package keys on it. The implementation moved to internal/clientip so
// internal/auth can hand Limen's own limiter the identical key (see auth.httpConfigOptions).
func ClientIP(r *http.Request, trustProxy bool) string {
	return clientip.FromRequest(r, trustProxy)
}
```

and adjust the imports (`net`/`strings` may become unused in that file — remove them; add `"github.com/refsdal/whenweall/internal/clientip"`).

In `internal/auth/auth.go`, add `"github.com/refsdal/whenweall/internal/clientip"` to the imports and replace the tail of `httpConfigOptions` — from the `// EnableTestRoutes means /api/test/seed …` comment through `return opts` — with:

```go
	// Limen's own built-in limiter (NewDefaultRateLimiterConfig, unconditionally wired in whenever
	// WithHTTPRateLimiter is absent) has two defaults this deployment cannot live with:
	//
	//   - Its key generator returns the raw X-Forwarded-For header verbatim (then X-Real-IP, then
	//     RemoteAddr — utils.go's ipExtractorFromRemoteAddr). Without a trusted proxy that header
	//     is attacker-controlled, so its 5-per-10s sign-in rule could be bypassed by varying the
	//     header, or aimed at a victim by forging their address. Keying on clientip.FromRequest —
	//     the same value internal/httpserver's own Postgres limiter uses — makes both limiters
	//     agree on who the client is and honour TRUST_PROXY the same way.
	//   - Its global 100/min-per-key rule covers every route, including GET /me and
	//     GET /organizations/active, which the SPA reads on every navigation; a NATed office
	//     would start seeing 429s from /me. Those two are read-only and cheap, and our own
	//     limiter still guards the hot mutating routes, so they are exempted outright.
	limiterOpts := []limen.RateLimiterOption{
		limen.WithRateLimiterKeyGenerator(func(r *http.Request) string {
			return clientip.FromRequest(r, cfg.TrustProxy)
		}),
		// Paths are joined onto the HTTP base path by Limen (path.Join("/api/v1/auth", "/me")).
		limen.WithRateLimiterDisableForPaths("/me", "/organizations/active"),
	}

	// EnableTestRoutes means /api/test/seed (internal/httpserver's Task 5 route) is live, and
	// with it, e2e specs signing up a fresh user per fixture against ONE long-lived server
	// process. Limen's limiter is a single in-memory bucket per (IP, path) — credential-password's
	// own PluginHTTPConfig caps /signup/credential and /signin/credential at 5 requests/10s each,
	// which a real Playwright run blows through in seconds (one seed call per fixture, one
	// sign-in per spec) regardless of how many distinct e2e users those requests are for. A
	// deployment that has already accepted "the seed route resets/creates data on demand"
	// (EnableTestRoutes's whole premise, config.Load's own hard-fail keeps this off production)
	// has no reason to also defend Limen's routes against its OWN test traffic, so this disables
	// Limen's rate limiter outright rather than trying to raise its ceiling high enough to guess
	// right for an unknown suite size.
	if cfg.EnableTestRoutes {
		limiterOpts = append(limiterOpts, limen.WithRateLimiterEnabled(false))
	}
	opts = append(opts, limen.WithHTTPRateLimiter(limiterOpts...))

	return opts
```

- [ ] **Step 4: Run the tests**

Run: `go build ./... && go test ./internal/clientip/ ./internal/auth/ ./internal/httpserver/`
Expected: all `ok` (httpserver's existing `TestClientIP*` cases still pass through the wrapper).

- [ ] **Step 5: Lint and commit**

Run: `go vet ./... && golangci-lint run ./...`
Expected: no findings.

```bash
git add internal/clientip internal/httpserver/ratelimit.go internal/auth/auth.go internal/auth/ratelimit_test.go
git commit -m "fix(auth): key Limen's rate limiter on the trusted client IP; exempt /me

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 9: Server-side captcha on sign-in, sign-up and password-reset request

**Files:**
- Create: `internal/httpserver/captcha.go`, `internal/httpserver/captcha_test.go`
- Modify: `internal/httpserver/server.go` (`routes()`)

**Interfaces:**
- Consumes: `VerifyTurnstile` (turnstile.go), `ClientIP`, `cfg.Capabilities.Turnstile`, `cfg.TurnstileSecretKey`.
- Produces: when Turnstile is configured, `POST /api/v1/auth/signin/credential`, `POST /api/v1/auth/signup/credential` and `POST /api/v1/auth/passwords/request-reset` require a valid `X-Captcha-Token` header, else `403 {"error":{"code":"captcha_failed"}}`. When Turnstile is not configured the middleware is a no-op (spec §8: unset = invisible, never broken).

- [ ] **Step 1: Write the failing tests**

Create `internal/httpserver/captcha_test.go`:

```go
package httpserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// turnstileConfig is testConfig plus a configured Turnstile pair (so cfg.Capabilities.Turnstile
// is true — config.Load derives it from the pair).
func turnstileConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "http://localhost:3000", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
		"TURNSTILE_SITE_KEY": "site", "TURNSTILE_SECRET_KEY": "secret",
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Capabilities.Turnstile {
		t.Fatal("test config did not enable the Turnstile capability")
	}
	return cfg
}

func postAuth(t *testing.T, ts *httptest.Server, path string, body map[string]any, captcha string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if captcha != "" {
		req.Header.Set("X-Captcha-Token", captcha)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func readCode(t *testing.T, resp *http.Response) (int, string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body.Error.Code
}

func TestAuthCaptcha_RequiredOnHotRoutesWhenConfigured(t *testing.T) {
	// siteverify accepts exactly the token "good".
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": r.PostFormValue("response") == "good"})
	}))
	withSiteverifyStub(t, stub)

	d := testdb.New(t)
	cfg := turnstileConfig(t)
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, path := range []string{
		"/api/v1/auth/signup/credential",
		"/api/v1/auth/signin/credential",
		"/api/v1/auth/passwords/request-reset",
	} {
		body := map[string]any{"email": "cap@example.com", "credential": "cap@example.com", "password": "Str0ngPassw0rd"}

		status, code := readCode(t, postAuth(t, ts, path, body, ""))
		if status != http.StatusForbidden || code != "captcha_failed" {
			t.Errorf("%s without token: %d %q, want 403 captcha_failed", path, status, code)
		}
		status, code = readCode(t, postAuth(t, ts, path, body, "bad"))
		if status != http.StatusForbidden || code != "captcha_failed" {
			t.Errorf("%s with rejected token: %d %q, want 403 captcha_failed", path, status, code)
		}
		status, code = readCode(t, postAuth(t, ts, path, body, "good"))
		if code == "captcha_failed" {
			t.Errorf("%s with accepted token: still %d captcha_failed — request never reached Limen", path, status)
		}
	}

	// Anything else under the mount is untouched: a bogus verify-email token gets Limen's own
	// 4xx, never captcha_failed.
	status, code := readCode(t, postAuth(t, ts, "/api/v1/auth/verify-email", map[string]any{"token": "x"}, ""))
	if code == "captcha_failed" || status/100 == 2 {
		t.Errorf("verify-email: %d %q, want Limen's own rejection", status, code)
	}
}

func TestAuthCaptcha_NoOpWhenTurnstileUnconfigured(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig() // no TURNSTILE_* → capability off
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	status, code := readCode(t, postAuth(t, ts, "/api/v1/auth/signup/credential",
		map[string]any{"email": "nocap@example.com", "password": "Str0ngPassw0rd"}, ""))
	if status/100 != 2 || code == "captcha_failed" {
		t.Fatalf("signup without captcha configured: %d %q, want 2xx", status, code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/httpserver/ -run TestAuthCaptcha`
Expected: `TestAuthCaptcha_RequiredOnHotRoutesWhenConfigured` FAILs (`without token: 200 "" want 403 captcha_failed` for signup); the no-op test passes already.

- [ ] **Step 3: Implement the middleware**

Create `internal/httpserver/captcha.go`:

```go
package httpserver

import (
	"net/http"
	"path"
	"strings"
)

// authCaptchaRoutes are the three unauthenticated, bot-attractive Limen routes that must carry a
// solved Turnstile challenge when the capability is on — the same set Better-Auth's captcha
// plugin protected in the TS stack (sign-up, sign-in, password-reset request). The e-mail
// verification resend is protected by a session instead and needs no captcha. Keyed exactly like
// authRateLimitedRoutes: "METHOD canonical-path".
var authCaptchaRoutes = map[string]struct{}{
	"POST /api/v1/auth/signin/credential":       {},
	"POST /api/v1/auth/signup/credential":       {},
	"POST /api/v1/auth/passwords/request-reset": {},
}

// authCaptchaMiddleware verifies X-Captcha-Token (Cloudflare Turnstile's response token — the
// same header RequireCaptchaIfAnon reads for guest votes/comments/bookings) on authCaptchaRoutes
// and answers 403 captcha_failed when it is missing or rejected. Returns next unchanged when
// Turnstile is not configured: a deployment without the key pair cannot verify anything, so it
// must not demand it (spec §8: an unset capability is invisible, never broken). The SPA mirrors
// this with useCaptchaEnabled(): no site key → no widget → no header.
func (s *Server) authCaptchaMiddleware(next http.Handler) http.Handler {
	if !s.cfg.Capabilities.Turnstile {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleaned := path.Clean(strings.TrimSuffix(r.URL.Path, "/"))
		if _, ok := authCaptchaRoutes[r.Method+" "+cleaned]; !ok {
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-Captcha-Token")
		if err := VerifyTurnstile(r.Context(), s.cfg.TurnstileSecretKey, token, ClientIP(r, s.cfg.TrustProxy)); err != nil {
			Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

In `server.go`'s `routes()`, after `guardedAuthHandler := s.authSvc.AuthMountGuard(authHandler)` insert:

```go
	// Captcha sits between the rate limit and the guard: cheapest check first (a counter), then
	// the network round trip to Turnstile, then Limen. Not skipped under EnableTestRoutes — the
	// e2e suite configures Cloudflare's always-pass test keys and exercises the real header.
	captchaAuthHandler := s.authCaptchaMiddleware(guardedAuthHandler)
```

and change the following two lines to use it:

```go
	authRouteHandler := captchaAuthHandler
	if !s.cfg.EnableTestRoutes {
		authRouteHandler = s.authRateLimitMiddleware(captchaAuthHandler)
	}
```

Also update the stale comment at the top of `turnstile.go` (lines 3-8): replace `requireTurnstile (throws CAPTCHA_FAILED …) is ported as requireCaptchaIfAnon (internal/polls/handlers.go) instead of the middleware this package briefly also carried (RequireCaptcha, removed as dead code — nothing ever wired it into a route; every public+captcha check in the actual HTTP surface calls VerifyTurnstile directly through requireCaptchaIfAnon).` with `requireTurnstile (throws CAPTCHA_FAILED when verification doesn't succeed) is ported twice: RequireCaptchaIfAnon (domainauth.go) for guest votes/comments/bookings, and Server.authCaptchaMiddleware (captcha.go) for the sign-in/sign-up/password-reset routes under the Limen mount.`

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/httpserver/`
Expected: `ok`.

- [ ] **Step 5: Lint and commit**

```bash
go vet ./... && golangci-lint run ./...
git add internal/httpserver/captcha.go internal/httpserver/captcha_test.go internal/httpserver/server.go internal/httpserver/turnstile.go
git commit -m "fix(http): verify Turnstile server-side on sign-in, sign-up and reset request

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 10: OIDC verified-email guard, org-slug hooks, personal-org slug cap

**Files:**
- Create: `internal/auth/oidc_guard.go`, `internal/auth/oidc_guard_test.go`, `internal/auth/orgslug.go`, `internal/auth/orgslug_test.go`
- Modify: `internal/auth/auth.go` (`buildLimenConfig`: org hooks + `oauth.WithGetUserInfo`; `personalOrgSlug`), `internal/bookings/schemas.go:276-289` (`validateHandle` delegates), `README.md` (OIDC rows)

**Interfaces:**
- Consumes: `oauth.Provider`, `oauth.WithGetUserInfo`, `oauth.ProviderUserInfo`, `oauth.TokenResponse`; `organization.WithHooks`, `organization.Hooks`, `organization.CreateOrganizationRequest`, `organization.UpdateOrganizationRequest`; `limen.NewLimenError`.
- Produces: `auth.ValidateOrgSlug(slug string) error` (`ErrInvalidOrgSlug` sentinel; rule: `^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`, i.e. 3–30 chars); Limen's `POST/PATCH /organizations` reject bad slugs with 422; `personalOrgSlug` always satisfies the rule; the generic OIDC provider refuses a sign-in/link whose IdP userinfo lacks `email_verified: true`.

- [ ] **Step 1: Write the failing tests**

Create `internal/auth/orgslug_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/organization"
)

func TestValidateOrgSlug(t *testing.T) {
	for _, ok := range []string{"abc", "team-ada", "a1-b2-c3", strings.Repeat("a", 30)} {
		if err := ValidateOrgSlug(ok); err != nil {
			t.Errorf("ValidateOrgSlug(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "ab", "Team", "team ada", "-team", "team-", "team_ada", "Foo Bar!!", strings.Repeat("a", 31)} {
		if err := ValidateOrgSlug(bad); !errors.Is(err, ErrInvalidOrgSlug) {
			t.Errorf("ValidateOrgSlug(%q) = %v, want ErrInvalidOrgSlug", bad, err)
		}
	}
}

func TestPersonalOrgSlugAlwaysSatisfiesTheHandleRule(t *testing.T) {
	cases := []struct{ email, userID string }{
		{"ada@example.com", "1"},
		{"a.very.long.email.local.part.that.goes.on@example.com", "1234567"},
		{"---@example.com", "42"},
		{"Ünïcödé.Náme@example.com", "9"},
		{"x@example.com", "99999999999"},
	}
	for _, tc := range cases {
		slug := personalOrgSlug(tc.email, tc.userID)
		if err := ValidateOrgSlug(slug); err != nil {
			t.Errorf("personalOrgSlug(%q, %q) = %q: %v", tc.email, tc.userID, slug, err)
		}
		if !strings.HasSuffix(slug, "-"+tc.userID) {
			t.Errorf("personalOrgSlug(%q, %q) = %q, want the -<userID> suffix", tc.email, tc.userID, slug)
		}
	}
}

func TestOrgRoutesRejectInvalidSlugs(t *testing.T) {
	ts := newTestService(t)
	email := "slug-owner@example.com"
	ts.signUpVerifiedAndSignIn(t, email)
	triggerSessionResolution(t, ts)

	resp := ts.postJSON(t, "/api/v1/auth/organizations/", map[string]any{"name": "Bad", "slug": "Foo Bar!!"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 422 {
		t.Fatalf("create with invalid slug: status %d, want 422", resp.StatusCode)
	}
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/", map[string]any{"name": "Good", "slug": "good-slug"}), "create with valid slug")

	// The update hook, through the plugin API (the HTTP PATCH route takes an id the list route
	// never returns — see routes.txt).
	user := &limen.User{ID: lookupUserID(t, ts, email), Email: email}
	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	var good *organization.Organization
	for _, o := range page.Items {
		if o.Slug == "good-slug" {
			good = o
		}
	}
	if good == nil {
		t.Fatalf("good-slug not found among %d orgs", len(page.Items))
	}
	bad := "Nope!!"
	if _, err := ts.svc.orgs.UpdateOrganization(context.Background(), user, good.ID, &organization.UpdateOrganizationRequest{Slug: &bad}); err == nil {
		t.Error("UpdateOrganization with invalid slug succeeded, want an error")
	}
	fine := "renamed-slug"
	if _, err := ts.svc.orgs.UpdateOrganization(context.Background(), user, good.ID, &organization.UpdateOrganizationRequest{Slug: &fine}); err != nil {
		t.Errorf("UpdateOrganization with valid slug: %v", err)
	}
}
```

Create `internal/auth/oidc_guard_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/oauth2"

	"github.com/thecodearcher/limen/plugins/oauth"
)

type fakeProvider struct {
	name string
	info *oauth.ProviderUserInfo
	err  error
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) OAuth2Config() (*oauth2.Config, []oauth2.AuthCodeOption) {
	return &oauth2.Config{}, nil
}
func (f *fakeProvider) GetUserInfo(context.Context, *oauth.TokenResponse) (*oauth.ProviderUserInfo, error) {
	return f.info, f.err
}

func TestVerifiedEmailUserInfoGuardsOnlyTheNamedProvider(t *testing.T) {
	google := &fakeProvider{name: "google", info: &oauth.ProviderUserInfo{ID: "g1", Email: "g@example.com", EmailVerified: false}}
	sso := &fakeProvider{name: "sso", info: &oauth.ProviderUserInfo{ID: "s1", Email: "s@example.com", EmailVerified: false}}
	fn := verifiedEmailUserInfo([]oauth.Provider{google, sso}, "sso")
	ctx := context.Background()

	if _, err := fn(ctx, "google", &oauth.TokenResponse{}); err != nil {
		t.Errorf("google (unguarded) returned %v, want the provider's own info", err)
	}
	if _, err := fn(ctx, "sso", &oauth.TokenResponse{}); !errors.Is(err, ErrOIDCEmailUnverified) {
		t.Errorf("sso with email_verified=false returned %v, want ErrOIDCEmailUnverified", err)
	}

	sso.info.EmailVerified = true
	info, err := fn(ctx, "sso", &oauth.TokenResponse{})
	if err != nil || info == nil || info.Email != "s@example.com" {
		t.Errorf("sso with email_verified=true returned (%+v, %v), want the provider's info", info, err)
	}

	sso.err = errors.New("idp down")
	if _, err := fn(ctx, "sso", &oauth.TokenResponse{}); err == nil || errors.Is(err, ErrOIDCEmailUnverified) {
		t.Errorf("provider error was %v, want it passed through untouched", err)
	}
	if _, err := fn(ctx, "unknown", &oauth.TokenResponse{}); err == nil {
		t.Error("unknown provider returned nil error")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'TestValidateOrgSlug|TestPersonalOrgSlug|TestOrgRoutesRejectInvalidSlugs|TestVerifiedEmailUserInfo'`
Expected: FAIL to compile (`ValidateOrgSlug`, `ErrInvalidOrgSlug`, `verifiedEmailUserInfo`, `ErrOIDCEmailUnverified` undefined). `golang.org/x/oauth2` is already an indirect dependency via Limen's oauth plugin; if `go test` complains it is missing from go.mod, run `go get golang.org/x/oauth2@$(go list -m -f '{{.Version}}' golang.org/x/oauth2)` and commit go.mod/go.sum with this task.

- [ ] **Step 3: Implement the slug rule and hooks**

Create `internal/auth/orgslug.go`:

```go
package auth

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/organization"
)

// ErrInvalidOrgSlug: the slug is not 3–30 lowercase ASCII letters, digits or hyphens starting and
// ending with a letter or digit.
var ErrInvalidOrgSlug = errors.New("auth: organization slug must be 3-30 lowercase letters, digits or hyphens")

// orgSlugRegexp is the handle rule (HANDLE_SLUG_RE in the TS source): the pattern itself enforces
// the 3–30 length (1 + 1..28 + 1). internal/bookings.validateHandle delegates here so the public
// /book/{handle} segment and Limen's organization slug can never disagree about what is allowed.
var orgSlugRegexp = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`)

// ValidateOrgSlug reports ErrInvalidOrgSlug unless slug satisfies orgSlugRegexp.
func ValidateOrgSlug(slug string) error {
	if !orgSlugRegexp.MatchString(slug) {
		return ErrInvalidOrgSlug
	}
	return nil
}

// orgSlugLimenError is what the organization hooks return so Limen's responder answers 422 with a
// readable message (a plain error would become a 500).
func orgSlugLimenError() error {
	return limen.NewLimenError("slug must be 3-30 lowercase letters, digits or hyphens, starting and ending with a letter or digit", http.StatusUnprocessableEntity, nil)
}

// organizationHooks enforces the slug rule on Limen's own organization routes (POST /organizations/,
// PATCH /organizations/:id), which otherwise only TrimSpace the slug (normalizeSlugs is off) —
// without this a raw PATCH could store "Foo Bar!!" as the public /book/<handle> segment. Both
// hooks run AFTER Limen's own slug generation/normalization (organizations.go), so request.Slug is
// the final value. The personal-org creation path goes through CreateOrganization too, which is
// why personalOrgSlug caps its length (see its doc comment).
func organizationHooks() organization.Hooks {
	return organization.Hooks{
		BeforeCreateOrganization: func(_ context.Context, _ *limen.User, req *organization.CreateOrganizationRequest) error {
			if err := ValidateOrgSlug(req.Slug); err != nil {
				return orgSlugLimenError()
			}
			return nil
		},
		BeforeUpdateOrganization: func(_ context.Context, _ *limen.User, _ *organization.Organization, req *organization.UpdateOrganizationRequest) error {
			if req.Slug != nil {
				if err := ValidateOrgSlug(*req.Slug); err != nil {
					return orgSlugLimenError()
				}
			}
			return nil
		},
	}
}
```

In `auth.go`'s `buildLimenConfig`, change the organization plugin construction to:

```go
		organization.New(
			organization.WithSendInvitationMail(func(ctx context.Context, d *organization.SendInvitationMailData) {
				s.enqueueInviteMail(ctx, d)
			}),
			organization.WithHooks(organizationHooks()),
		),
```

Replace `personalOrgSlug` (auth.go:435-456) with:

```go
// maxOrgSlugLen is the upper bound ValidateOrgSlug enforces.
const maxOrgSlugLen = 30

// personalOrgSlug derives a per-user-unique slug from email's local part and userID: lowercased,
// every run of non-alphanumeric characters collapsed to a single '-', truncated so that
// local + "-" + userID never exceeds maxOrgSlugLen, with leading/trailing hyphens trimmed so the
// result satisfies ValidateOrgSlug (which organizationHooks now enforces on every create —
// including this one). Uniqueness comes from userID, not the local part alone — see
// createPersonalOrgIfMissing's doc comment for why that matters. The slug handed to
// CreateOrganization is already exactly what ends up stored (normalizeSlugs is off).
func personalOrgSlug(email, userID string) string {
	local := nameFromEmail(email)
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(local) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			b.WriteRune('-')
			lastHyphen = true
		}
	}
	normalized := b.String()

	maxLocal := maxOrgSlugLen - 1 - len(userID)
	if maxLocal < 1 {
		maxLocal = 1
	}
	if len(normalized) > maxLocal {
		normalized = normalized[:maxLocal]
	}
	normalized = strings.Trim(normalized, "-")
	if normalized == "" {
		normalized = "org"
		if len(normalized) > maxLocal {
			normalized = normalized[:maxLocal]
		}
	}
	return normalized + "-" + userID
}
```

In `internal/bookings/schemas.go`, replace `validateHandle` (lines 276-289) with:

```go
// validateHandle ports handleSchema — the org-slug ("handle") counterpart of a page's own slug,
// used by SetOrgSlug. The rule itself lives in internal/auth.ValidateOrgSlug (Limen's organization
// hooks enforce the identical rule on its own routes, so the two can't drift); this reports it
// under the "handle" field key the org-handle HTTP endpoint's request body uses.
func validateHandle(handle string) error {
	if err := auth.ValidateOrgSlug(handle); err != nil {
		return &ValidationError{Fields: map[string]string{
			"handle": "handle must be lowercase letters, digits and hyphens, 3-30 characters",
		}}
	}
	return nil
}
```

and add `"github.com/refsdal/whenweall/internal/auth"` to that file's imports (internal/bookings already depends on internal/auth via authz.go, so no cycle). Keep `handleSlugRegexp`/`LimitHandleMin`/`LimitHandleMax` — the page-slug validation at schemas.go:105 still uses them.

- [ ] **Step 4: Implement the OIDC guard**

Create `internal/auth/oidc_guard.go`:

```go
package auth

import (
	"context"
	"net/http"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/oauth"
)

// ErrOIDCEmailUnverified is returned to Limen's OAuth callback when the generic OIDC provider's
// userinfo does not assert email_verified. Limen turns it into the callback's error redirect
// (?error=...), so the browser lands back on the SPA with a message instead of a session.
var ErrOIDCEmailUnverified = limen.NewLimenError("your identity provider did not report a verified email address", http.StatusForbidden, nil)

// verifiedEmailUserInfo builds the oauth.WithGetUserInfo override: it delegates to the named
// provider's own GetUserInfo (so Google, and the generic provider's id_token/userinfo handling,
// keep working exactly as before) and, for guardedProvider only, refuses a profile whose
// EmailVerified is false.
//
// Why: Limen's CreateOrLinkAccount links a provider account to an EXISTING user found by email
// with no verified-email check (plugins/oauth account_linker.go). With Google that is fine — it
// only ever asserts verified addresses — but a bring-your-own OIDC issuer (Authentik, Keycloak)
// may let its users set an unverified email, and anyone who can present victim@example.com from
// that IdP would take over the victim's password account. Refusing at userinfo time is the
// earliest fork-free point: nothing is created or linked yet.
func verifiedEmailUserInfo(providers []oauth.Provider, guardedProvider string) func(context.Context, string, *oauth.TokenResponse) (*oauth.ProviderUserInfo, error) {
	byName := make(map[string]oauth.Provider, len(providers))
	for _, p := range providers {
		byName[p.Name()] = p
	}
	return func(ctx context.Context, providerName string, token *oauth.TokenResponse) (*oauth.ProviderUserInfo, error) {
		p, ok := byName[providerName]
		if !ok {
			return nil, limen.NewLimenError("unknown oauth provider", http.StatusNotFound, nil)
		}
		info, err := p.GetUserInfo(ctx, token)
		if err != nil {
			return nil, err
		}
		if providerName == guardedProvider && (info == nil || !info.EmailVerified) {
			return nil, ErrOIDCEmailUnverified
		}
		return info, nil
	}
}
```

In `buildLimenConfig`, change the oauth plugin line to:

```go
		plugins = append(plugins, oauth.New(
			oauth.WithProviders(providers...),
			// See verifiedEmailUserInfo: only the generic OIDC provider is guarded; when OIDC is
			// off, cfg.OIDCName matches no provider and this is a pure pass-through.
			oauth.WithGetUserInfo(verifiedEmailUserInfo(providers, cfg.OIDCName)),
		))
```

- [ ] **Step 5: README**

In `README.md`'s configuration table, change the `OIDC_ISSUER` row's description to:

```
| `OIDC_ISSUER`           | no       | —                                  | Optional external SSO. Needs `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` too (all three, not a pair). The issuer must assert `email_verified: true` in its userinfo/ID token: a sign-in whose email is not verified by the IdP is refused, because an OIDC email is what links the sign-in to an existing account. |
```

- [ ] **Step 6: Run the tests**

Run: `go build ./... && go test ./internal/auth/ ./internal/bookings/`
Expected: all `ok` (bookings' handle tests still pass — same rule).

- [ ] **Step 7: Lint and commit**

```bash
go vet ./... && golangci-lint run ./...
git add internal/auth internal/bookings/schemas.go README.md go.mod go.sum
git commit -m "fix(auth): OIDC verified-email guard, org slug rules on Limen routes, slug cap

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

(Drop `go.mod go.sum` from the `git add` if Step 2 needed no `go get`.)

---

### Task 11: Go test for the organization invitation flow

**Files:**
- Create: `internal/auth/invitation_test.go`

**Interfaces:**
- Consumes: Limen org routes (`POST /organizations/invitations`, `GET /organizations/invitations/token/:token`, `POST /organizations/invitations/respond`), `enqueueInviteMail` (Task 4), `SetProfile` (Task 3), `ListUserOrganizations` (Task 6).
- Produces: nothing new — pins the invite → mail → accept → membership chain that had zero Go coverage.

- [ ] **Step 1: Write the test**

```go
package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// TestInvitationFlow drives the whole chain a real team goes through: the owner (with a stored
// name and locale) invites by email through Limen's route, our WithSendInvitationMail callback
// enqueues an org_invite mail whose link carries the token, the invitee signs up with that
// address, reads the invitation by token and accepts it, and ends up a member of the owner's
// organization.
func TestInvitationFlow(t *testing.T) {
	ts := newTestService(t)
	ctx := context.Background()

	ownerEmail := "owner-invites@example.com"
	ts.signUpVerifiedAndSignIn(t, ownerEmail)
	triggerSessionResolution(t, ts) // personal org exists and is the active org
	ownerID := fmt.Sprint(lookupUserID(t, ts, ownerEmail))
	ownerName, nb := "Ada Lovelace", "nb"
	if err := ts.svc.SetProfile(ctx, ownerID, &ownerName, &nb); err != nil {
		t.Fatalf("SetProfile(owner): %v", err)
	}

	inviteeEmail := "invitee@example.com"
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/invitations", map[string]any{
		"email": inviteeEmail, "role": "member",
	}), "invite")

	msg, ok := ts.mail.find("org_invite")
	if !ok {
		t.Fatal("no org_invite mail captured")
	}
	if msg.To != inviteeEmail {
		t.Errorf("org_invite To = %q, want %q", msg.To, inviteeEmail)
	}
	if got, _ := msg.Data["InviterName"].(string); got != ownerName {
		t.Errorf("Data.InviterName = %q, want %q (the stored display name, not the email local part)", got, ownerName)
	}
	if got, _ := msg.Data["Locale"].(string); got != "nb" {
		t.Errorf("Data.Locale = %q, want nb (inviter's locale — the invitee has no account yet)", got)
	}
	url, _ := msg.Data["URL"].(string)
	const prefix = "http://app.example/accept-invitation/"
	if !strings.HasPrefix(url, prefix) || url == prefix {
		t.Fatalf("org_invite URL = %q, want %q plus a token", url, prefix)
	}
	token := strings.TrimPrefix(url, prefix)

	// The invitee: a separate browser (cookie jar) against the same server.
	invitee := &testService{svc: ts.svc, mail: ts.mail, server: ts.server}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	invitee.client = &http.Client{Jar: jar}
	invitee.signUpVerifiedAndSignIn(t, inviteeEmail)
	triggerSessionResolution(t, invitee)

	inv := decodeJSON(t, invitee.get(t, "/api/v1/auth/organizations/invitations/token/"+token))
	if got, _ := inv["email"].(string); got != inviteeEmail {
		t.Errorf("invitation.email = %q, want %q (body %+v)", got, inviteeEmail, inv)
	}
	org, _ := inv["organization"].(map[string]any)
	ownerOrgSlug, _ := org["slug"].(string)
	if ownerOrgSlug == "" {
		t.Fatalf("invitation carries no organization.slug: %+v", inv)
	}

	requireStatus2xx(t, invitee.postJSON(t, "/api/v1/auth/organizations/invitations/respond", map[string]any{
		"token": token, "response": "accept",
	}), "accept")

	var members int
	if err := ts.svc.db.QueryRowContext(ctx, `
		SELECT count(*) FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		WHERE o.slug = $1 AND m.user_id = $2
	`, ownerOrgSlug, lookupUserID(t, ts, inviteeEmail)).Scan(&members); err != nil {
		t.Fatalf("counting membership: %v", err)
	}
	if members != 1 {
		t.Fatalf("membership rows for invitee in %q = %d, want 1", ownerOrgSlug, members)
	}

	orgs, err := ts.svc.ListUserOrganizations(ctx, &Session{UserID: fmt.Sprint(lookupUserID(t, ts, inviteeEmail))})
	if err != nil {
		t.Fatalf("ListUserOrganizations(invitee): %v", err)
	}
	var joined bool
	for _, o := range orgs {
		if o.Slug == ownerOrgSlug {
			joined = true
		}
	}
	if !joined || len(orgs) != 2 {
		t.Errorf("invitee orgs = %+v, want their personal org plus %q", orgs, ownerOrgSlug)
	}

	// Accepting twice is refused (the invitation is no longer pending).
	again := invitee.postJSON(t, "/api/v1/auth/organizations/invitations/respond", map[string]any{
		"token": token, "response": "accept",
	})
	defer func() { _ = again.Body.Close() }()
	if again.StatusCode/100 == 2 {
		t.Error("second accept succeeded, want a 4xx")
	}
}

// TestInvitationRejectsMismatchedEmail: whoever holds the link still needs an account under the
// invited address (Limen string-compares invitation.Email with the session user's email).
func TestInvitationRejectsMismatchedEmail(t *testing.T) {
	ts := newTestService(t)
	ts.signUpVerifiedAndSignIn(t, "owner2@example.com")
	triggerSessionResolution(t, ts)
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/organizations/invitations", map[string]any{
		"email": "right-person@example.com", "role": "member",
	}), "invite")
	msg, _ := ts.mail.find("org_invite")
	token := strings.TrimPrefix(msg.Data["URL"].(string), "http://app.example/accept-invitation/")

	stranger := &testService{svc: ts.svc, mail: ts.mail, server: ts.server}
	jar, _ := cookiejar.New(nil)
	stranger.client = &http.Client{Jar: jar}
	stranger.signUpVerifiedAndSignIn(t, "wrong-person@example.com")
	triggerSessionResolution(t, stranger)

	resp := stranger.postJSON(t, "/api/v1/auth/organizations/invitations/respond", map[string]any{
		"token": token, "response": "accept",
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 == 2 {
		t.Fatal("a different account accepted someone else's invitation")
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/auth/ -run TestInvitation -v`
Expected: both PASS (this is a characterization test of code that already exists; if `Data.InviterName` or `Data.Locale` fail, Task 4 was not applied). If Limen answers the invite with 422 because the role name differs, read `plugins/organization@…/defaults.go:8-10` (`owner`/`admin`/`member`) and fix the test body, not the code.

- [ ] **Step 3: Commit**

```bash
git add internal/auth/invitation_test.go
git commit -m "test(auth): pin the invitation flow end to end (invite, mail, accept, membership)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 12: Web API layer — `auth.ts` profile/org/captcha functions, guest-locale plumbing

**Files:**
- Modify: `web/src/api/auth.ts` (rewrite the top half + new functions), `web/src/api/polls.ts:209-292` (`addParticipant`, `updateParticipant`, `claimSlot`), `web/src/api/bookings.ts:236-255` (`bookSlot`), `web/src/api/__tests__/auth.test.ts`
- Create: `web/src/api/__tests__/guest-locale.test.ts`

**Interfaces:**
- Consumes: the Go routes from Tasks 4–7; `getLocale`/`isAppLocale` from `#/lib/i18n`; `appConfig` from `#/app.config`.
- Produces (exact exports of `web/src/api/auth.ts`):
  ```ts
  export type Locale = AppLocale
  export type AuthUser = { id: string; name: string; email: string; emailVerified: boolean; locale: Locale; hasPassword: boolean; isStaff: boolean }
  export function signInWithCredential(credential: string, password: string, captchaToken?: string | null): Promise<AuthUser>
  export function signUpWithCredential(email: string, password: string, name: string, captchaToken?: string | null): Promise<AuthUser>
  export function requestPasswordReset(email: string, captchaToken?: string | null): Promise<void>
  export function me(): Promise<AuthUser | null>           // null on 401 AND on 403 (locked)
  export function updateProfile(patch: { name?: string; locale?: string }): Promise<void>
  export function deleteOwnAccount(password?: string): Promise<void>
  export type OrgSummary = { id: string; name: string; slug: string; active: boolean }
  export function listOrganizations(): Promise<OrgSummary[]>
  export function switchOrganization(orgId: string): Promise<void>
  export function acceptInvitation(invitationToken: string): Promise<{ orgSlug: string | null }>
  ```
  plus the unchanged `signOut`, `resetPassword`, `verifyEmail`, `requestEmailVerification`, `oauthAuthorizeUrl`, `oauthLinkUrl`, `activeOrganization`, `myOrgRoles`. `addParticipant`/`updateParticipant`/`claimSlot`/`bookSlot` send `locale: getLocale()` in their bodies.

- [ ] **Step 1: Write the failing tests**

Replace the `me()` describe block in `web/src/api/__tests__/auth.test.ts` and add the new ones (keep the `myOrgRoles()` block and the msw setup as they are):

```ts
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import {
  acceptInvitation,
  deleteOwnAccount,
  listOrganizations,
  me,
  myOrgRoles,
  requestPasswordReset,
  signInWithCredential,
  signUpWithCredential,
  switchOrganization,
  updateProfile,
} from '#/api/auth'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

describe('me()', () => {
  it('reads the session transformer payload: id, name, locale, emailVerified, hasPassword, isStaff', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({
          user: {
            id: '42',
            email: 'ada@example.com',
            first_name: 'Ada',
            last_name: 'Lovelace',
            name: 'Ada Lovelace',
            locale: 'nb',
            emailVerified: true,
            hasPassword: true,
            isStaff: true,
          },
        }),
      ),
    )

    expect(await me()).toEqual({
      id: '42',
      name: 'Ada Lovelace',
      email: 'ada@example.com',
      emailVerified: true,
      locale: 'nb',
      hasPassword: true,
      isStaff: true,
    })
  })

  it('falls back to composed name, en locale and email_verified_at when the new fields are absent', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({
          user: { id: '1', email: 'a@example.com', first_name: 'A', email_verified_at: null },
        }),
      ),
    )

    const user = await me()

    expect(user?.name).toBe('A')
    expect(user?.locale).toBe('en')
    expect(user?.emailVerified).toBe(false)
    expect(user?.hasPassword).toBe(false)
    expect(user?.isStaff).toBe(false)
  })

  it('returns null for an anonymous caller (401)', async () => {
    server.use(http.get('/api/v1/auth/me', () => new HttpResponse(null, { status: 401 })))

    expect(await me()).toBeNull()
  })

  it('returns null for a locked account (403 from the auth mount guard) instead of throwing', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({ error: { code: 'forbidden', message: 'account is locked' } }, { status: 403 }),
      ),
    )

    expect(await me()).toBeNull()
  })
})

describe('captcha token forwarding', () => {
  it('sends X-Captcha-Token on sign-in only when a token is given', async () => {
    const seen: (string | null)[] = []
    server.use(
      http.post('/api/v1/auth/signin/credential', ({ request }) => {
        seen.push(request.headers.get('X-Captcha-Token'))
        return HttpResponse.json({ user: { id: '1', email: 'a@example.com' } })
      }),
    )

    await signInWithCredential('a@example.com', 'pw', 'tok')
    await signInWithCredential('a@example.com', 'pw', null)
    await signInWithCredential('a@example.com', 'pw')

    expect(seen).toEqual(['tok', null, null])
  })

  it('sign-up sends name, locale and the captcha token', async () => {
    let body: unknown
    let header: string | null = null
    server.use(
      http.post('/api/v1/auth/signup/credential', async ({ request }) => {
        body = await request.json()
        header = request.headers.get('X-Captcha-Token')
        return HttpResponse.json({ user: { id: '1', email: 'a@example.com' } })
      }),
    )

    await signUpWithCredential('a@example.com', 'pw', 'Ada Lovelace', 'tok')

    expect(body).toEqual({ email: 'a@example.com', password: 'pw', name: 'Ada Lovelace', locale: 'en' })
    expect(header).toBe('tok')
  })

  it('password-reset request forwards the token', async () => {
    let header: string | null = null
    server.use(
      http.post('/api/v1/auth/passwords/request-reset', ({ request }) => {
        header = request.headers.get('X-Captcha-Token')
        return HttpResponse.json('ok')
      }),
    )

    await requestPasswordReset('a@example.com', 'tok')

    expect(header).toBe('tok')
  })
})

describe('account routes', () => {
  it('updateProfile PATCHes /api/v1/me with only the given fields', async () => {
    let body: unknown
    server.use(
      http.patch('/api/v1/me', async ({ request }) => {
        body = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )

    await updateProfile({ locale: 'nb' })

    expect(body).toEqual({ locale: 'nb' })
  })

  it('deleteOwnAccount DELETEs /api/v1/me with the password when given', async () => {
    let body: unknown = 'unset'
    server.use(
      http.delete('/api/v1/me', async ({ request }) => {
        body = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )

    await deleteOwnAccount('hunter2hunter2')

    expect(body).toEqual({ password: 'hunter2hunter2' })
  })

  it('listOrganizations and switchOrganization use the /api/v1/me organization routes', async () => {
    let switched: unknown
    server.use(
      http.get('/api/v1/me/organizations', () =>
        HttpResponse.json([{ id: '7', name: 'Team', slug: 'team', active: false }]),
      ),
      http.post('/api/v1/me/active-organization', async ({ request }) => {
        switched = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )

    expect(await listOrganizations()).toEqual([{ id: '7', name: 'Team', slug: 'team', active: false }])
    await switchOrganization('7')
    expect(switched).toEqual({ orgId: '7' })
  })

  it('acceptInvitation reads the org slug by token, accepts, and returns the slug', async () => {
    const calls: string[] = []
    server.use(
      http.get('/api/v1/auth/organizations/invitations/token/tok-1', () => {
        calls.push('read')
        return HttpResponse.json({ email: 'a@example.com', organization: { name: 'Team', slug: 'team' } })
      }),
      http.post('/api/v1/auth/organizations/invitations/respond', async ({ request }) => {
        calls.push('respond:' + JSON.stringify(await request.json()))
        return HttpResponse.json({ status: 'accepted' })
      }),
    )

    expect(await acceptInvitation('tok-1')).toEqual({ orgSlug: 'team' })
    expect(calls).toEqual(['read', 'respond:{"token":"tok-1","response":"accept"}'])
  })
})

describe('myOrgRoles()', () => {
  it("returns the caller's role names in the active organization", async () => {
    server.use(
      http.get('/api/v1/auth/organizations/me', () => HttpResponse.json({ roles: ['owner'] })),
    )

    expect(await myOrgRoles()).toEqual(['owner'])
  })

  it('returns [] when there is no active organization (403)', async () => {
    server.use(
      http.get('/api/v1/auth/organizations/me', () =>
        HttpResponse.json({ error: { code: 'no_active_org', message: 'no active org' } }, { status: 403 }),
      ),
    )

    expect(await myOrgRoles()).toEqual([])
  })
})
```

Create `web/src/api/__tests__/guest-locale.test.ts`:

```ts
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { addParticipant, claimSlot, updateParticipant } from '#/api/polls'
import { bookSlot } from '#/api/bookings'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

// Every guest-facing mutation carries the visitor's UI locale so the Go side can localize the
// mail it sends them (internal/polls and internal/bookings already accept `locale`). In vitest
// paraglide resolves the base locale, "en".
describe('guest forms send locale', () => {
  it('addParticipant', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.post('/api/v1/polls/p1/participants', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ participantId: 'x' })
      }),
    )
    await addParticipant('p1', { name: 'Ada', answers: {} })
    expect(body.locale).toBe('en')
  })

  it('updateParticipant', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.patch('/api/v1/polls/p1/participants/pa1', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await updateParticipant('p1', 'pa1', { answers: {} })
    expect(body.locale).toBe('en')
  })

  it('claimSlot', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.post('/api/v1/polls/p1/claims', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ participantId: 'x', claimedOptionIds: [] })
      }),
    )
    await claimSlot('p1', { optionId: 'o1', name: 'Ada' })
    expect(body.locale).toBe('en')
  })

  it('bookSlot', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.post('/api/v1/book/ada/intro/bookings', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ booking: { id: 'b1' }, manageToken: 't' })
      }),
    )
    await bookSlot('ada', 'intro', { startAt: '2026-09-15T07:00:00.000Z', name: 'Ada', email: 'a@example.com', timezone: 'Europe/Oslo' })
    expect(body.locale).toBe('en')
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && bunx vitest run src/api/__tests__/auth.test.ts src/api/__tests__/guest-locale.test.ts`
Expected: FAIL — `updateProfile is not a function`-style import errors and `expected undefined to be 'en'`.

- [ ] **Step 3: Rewrite the top of `web/src/api/auth.ts`**

Replace everything from `export type AuthUser = {` through the end of `requestPasswordReset` (lines 23-96) with:

```ts
import { appConfig, type AppLocale } from '#/app.config'
import { getLocale, isAppLocale } from '#/lib/i18n'

/** The app's locale union, re-exported under the name the rest of the auth surface uses. */
export type Locale = AppLocale

export type AuthUser = {
  id: string
  name: string
  email: string
  emailVerified: boolean
  locale: Locale
  /** Whether a password credential exists — the delete-account dialog asks for the current
   * password only when there is one (an OAuth-only account has nothing to re-enter). */
  hasPassword: boolean
  isStaff: boolean
}

/**
 * Limen's default user serialization would come back with NO usable id at all: `UserSchema.Serialize`
 * deletes the id column outright, and this backend configures no `WithPublicIDs` to replace it —
 * see `buildLimenConfig`'s own `sessionTransformer` (internal/auth/auth.go) for the fix, registered
 * via `limen.WithHTTPSessionTransformer`. Every signin/signup/me response is routed through that
 * transformer instead, which rebuilds the payload with `id` as a string of digits and adds
 * `isStaff` (staff_users), `name` (display name), `locale` (user_preferences), `emailVerified`
 * and `hasPassword` — all guaranteed present on every response `toAuthUser` sees. The fallbacks
 * below only matter for a payload from an older server during a rolling deploy.
 */
function toAuthUser(raw: Record<string, unknown>): AuthUser {
  const id = typeof raw.id === 'string' ? raw.id : ''
  const email = typeof raw.email === 'string' ? raw.email : ''
  const firstName = typeof raw.first_name === 'string' ? raw.first_name : ''
  const lastName = typeof raw.last_name === 'string' ? raw.last_name : ''
  const composedName = `${firstName} ${lastName}`.trim() || email
  const rawLocale = typeof raw.locale === 'string' ? raw.locale : ''
  return {
    id,
    name: typeof raw.name === 'string' && raw.name.length > 0 ? raw.name : composedName,
    email,
    emailVerified:
      typeof raw.emailVerified === 'boolean' ? raw.emailVerified : raw.email_verified_at != null,
    locale: isAppLocale(rawLocale) ? rawLocale : appConfig.defaultLocale,
    hasPassword: raw.hasPassword === true,
    isStaff: raw.isStaff === true,
  }
}

type SessionResponse = { user: Record<string, unknown> }

/** Turns the optional captcha token every auth form passes into the `api()` option — `null`/
 * `undefined` means "captcha is off or not solved", and then NO header is sent (the Go middleware
 * only demands one when Turnstile is configured). */
function captchaOpts(captchaToken?: string | null): { captchaToken?: string } {
  return captchaToken ? { captchaToken } : {}
}

export async function signInWithCredential(
  credential: string,
  password: string,
  captchaToken?: string | null,
): Promise<AuthUser> {
  const { user } = await api<SessionResponse>(
    'POST',
    '/api/v1/auth/signin/credential',
    { credential, password },
    captchaOpts(captchaToken),
  )
  return toAuthUser(user)
}

/**
 * Signup sends `name` and `locale` alongside Limen's own `email`/`password`: Limen ignores both,
 * but `internal/auth`'s After hook on the signup route (signup_hook.go) reads them off the same
 * body and stores them. Signup does NOT mint a session (auto-sign-in is off — the account is
 * unusable until the mailed verification link is clicked), so the returned user is informational.
 */
export async function signUpWithCredential(
  email: string,
  password: string,
  name: string,
  captchaToken?: string | null,
): Promise<AuthUser> {
  const { user } = await api<SessionResponse>(
    'POST',
    '/api/v1/auth/signup/credential',
    { email, password, name, locale: getLocale() },
    captchaOpts(captchaToken),
  )
  return toAuthUser(user)
}

export async function signOut(): Promise<void> {
  await api('POST', '/api/v1/auth/signout')
}

/**
 * `GET /api/v1/auth/me` — `null` for no session (401) AND for a locked account (403 from
 * internal/auth's AuthMountGuard: "account is locked"). Both mean "you are not signed in as far as
 * the UI is concerned"; the login page tells a locked user what happened (it signs in, then sees
 * `me()` come back null). Never throws for either case.
 */
export async function me(): Promise<AuthUser | null> {
  try {
    const { user } = await api<SessionResponse>('GET', '/api/v1/auth/me')
    return toAuthUser(user)
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) return null
    throw err
  }
}

export async function requestPasswordReset(email: string, captchaToken?: string | null): Promise<void> {
  await api('POST', '/api/v1/auth/passwords/request-reset', { email }, captchaOpts(captchaToken))
}
```

Move the two new `import` lines to the top of the file (after `import { api, ApiError } from '#/api/client'`). Then append the account/organization functions at the end of the file, and replace the existing `acceptInvitation`:

```ts
// ---- own account (internal/httpserver/account.go) -----------------------------------------

/** `PATCH /api/v1/me` — works before email verification too (an unverified user may still pick
 * the language of the verification mail we resend them). */
export async function updateProfile(patch: { name?: string; locale?: string }): Promise<void> {
  await api('PATCH', '/api/v1/me', patch)
}

/** `DELETE /api/v1/me` — a credential account must send its current password (400
 * `password_required` / 403 `invalid_password` otherwise); an OAuth-only account sends nothing. */
export async function deleteOwnAccount(password?: string): Promise<void> {
  await api('DELETE', '/api/v1/me', password ? { password } : {})
}

export type OrgSummary = { id: string; name: string; slug: string; active: boolean }

/** `GET /api/v1/me/organizations` — our own route, not Limen's `GET /organizations/`: Limen
 * serializes organizations WITHOUT an id (see routes.txt), and switching needs one. */
export function listOrganizations(): Promise<OrgSummary[]> {
  return api<OrgSummary[]>('GET', '/api/v1/me/organizations')
}

/** `POST /api/v1/me/active-organization` — membership is verified server-side (403 otherwise). */
export async function switchOrganization(orgId: string): Promise<void> {
  await api('POST', '/api/v1/me/active-organization', { orgId })
}

/**
 * Accepts an invitation: reads it by token first (`GET /organizations/invitations/token/:token`
 * embeds the organization's `slug`; the respond route does not), then `POST
 * /organizations/invitations/respond`. Returns the joined organization's slug so the caller can
 * find it in `listOrganizations()` and `switchOrganization()` to it — Limen's respond route does
 * not change the session's active organization. `invitationToken` is the `/accept-invitation/$id`
 * route param (the param IS the token).
 */
export async function acceptInvitation(invitationToken: string): Promise<{ orgSlug: string | null }> {
  const invitation = await api<{ organization?: { slug?: unknown } }>(
    'GET',
    `/api/v1/auth/organizations/invitations/token/${encodeURIComponent(invitationToken)}`,
  )
  const orgSlug = typeof invitation.organization?.slug === 'string' ? invitation.organization.slug : null
  await api('POST', '/api/v1/auth/organizations/invitations/respond', {
    token: invitationToken,
    response: 'accept',
  })
  return { orgSlug }
}
```

- [ ] **Step 4: Guest-locale plumbing in polls.ts and bookings.ts**

`web/src/api/polls.ts`: add `import { getLocale } from '#/lib/i18n'` to the imports, then:

- `addParticipant`: change the body to `{ name: input.name, email: input.email, answers: input.answers, locale: input.locale ?? getLocale() }`.
- `updateParticipant`: change the body argument `input` to `{ ...input, locale: getLocale() }`.
- `claimSlot`: add `locale: getLocale(),` after `email: input.email,` in the body object.

`web/src/api/bookings.ts`: add `import { getLocale } from '#/lib/i18n'`, and in `bookSlot` add `locale: getLocale(),` after `timezone: input.timezone,`.

- [ ] **Step 5: Run the tests, typecheck, lint**

Run: `cd web && bunx vitest run src/api && bun run typecheck && bun run lint`
Expected: all green. (Typecheck will flag every call site of `signUpWithCredential` — signup.tsx — for the new required `name` argument; that is fixed in Task 14. If you want the tree green at this commit, pass `name.trim()` there now: `await signUpWithCredential(email, password, name.trim())`.)

- [ ] **Step 6: Commit**

```bash
git add web/src/api web/src/routes/signup.tsx
git commit -m "feat(web): auth api gains profile, account deletion, org switch and captcha params

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 13: Captcha is optional in the SPA — `useCaptchaEnabled`, `TurnstileField` renders nothing when off, every gate follows

**Files:**
- Create: `web/src/lib/captcha.ts`, `web/src/lib/__tests__/captcha.test.tsx`
- Modify: `web/src/components/auth/TurnstileField.tsx`, `web/src/components/booking/BookingForm.tsx:104`, `web/src/components/signup/IdentitySheet.tsx:71`, `web/src/components/poll/use-answer-draft.ts:95`, `web/src/components/poll/Comments.tsx:73`, `web/src/routes/login.tsx:49`, `web/src/routes/signup.tsx:220`, `web/src/routes/forgot-password.tsx:115,162`
- Test: `web/src/components/auth/__tests__/TurnstileField.test.tsx`, `web/src/components/booking/__tests__/BookingForm.test.tsx`, `web/src/components/signup/__tests__/IdentitySheet.test.tsx`

**Interfaces:**
- Produces: `useTurnstileSiteKey(): string` and `useCaptchaEnabled(): boolean` in `#/lib/captcha` (true iff `publicConfig.turnstileSiteKey` is non-empty; false outside a router). Every captcha gate is `captchaEnabled && !captchaToken`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/__tests__/captcha.test.tsx`:

```tsx
import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useCaptchaEnabled, useTurnstileSiteKey } from '#/lib/captcha'

// Outside a RouterProvider there is no root context, hence no publicConfig, hence no site key —
// the same state a deployment without TURNSTILE_* is in. (The "enabled" branch is exercised by
// the component tests, which mock this module.)
describe('captcha capability hook', () => {
  it('reports captcha disabled and an empty site key when no config is reachable', () => {
    expect(renderHook(() => useTurnstileSiteKey()).result.current).toBe('')
    expect(renderHook(() => useCaptchaEnabled()).result.current).toBe(false)
  })
})
```

In `web/src/components/auth/__tests__/TurnstileField.test.tsx`, add after the `@marsidev/react-turnstile` mock:

```tsx
vi.mock('#/lib/captcha', () => ({
  useTurnstileSiteKey: vi.fn(() => 'site-key'),
  useCaptchaEnabled: vi.fn(() => true),
}))
```

add `import { useCaptchaEnabled, useTurnstileSiteKey } from '#/lib/captcha'` to the imports, and add this test inside the describe:

```tsx
  it('renders nothing at all when the deployment has no Turnstile site key', () => {
    vi.mocked(useTurnstileSiteKey).mockReturnValueOnce('')
    vi.mocked(useCaptchaEnabled).mockReturnValueOnce(false)
    const { container } = render(<TurnstileField onToken={vi.fn()} />)

    expect(container).toBeEmptyDOMElement()
    expect(screen.queryByRole('button', { name: 'mock turnstile' })).not.toBeInTheDocument()
  })
```

In `web/src/components/booking/__tests__/BookingForm.test.tsx`, add after the turnstile mock:

```tsx
vi.mock('#/lib/captcha', () => ({
  useTurnstileSiteKey: vi.fn(() => 'site-key'),
  useCaptchaEnabled: vi.fn(() => true),
}))
```

add `import { useCaptchaEnabled, useTurnstileSiteKey } from '#/lib/captcha'` to the imports, and add this test inside the describe:

```tsx
  it('submits without a captcha when Turnstile is not configured', async () => {
    vi.mocked(useCaptchaEnabled).mockReturnValue(false)
    vi.mocked(useTurnstileSiteKey).mockReturnValue('')
    try {
      const user = userEvent.setup()
      const { onSubmit } = renderForm()

      expect(screen.queryByRole('button', { name: 'mock turnstile' })).not.toBeInTheDocument()
      await user.type(screen.getByLabelText(/your name/i), 'Ada')
      await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
      await user.click(screen.getByRole('button', { name: /confirm booking/i }))

      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Ada', email: 'ada@example.com', turnstileToken: undefined }),
      )
    } finally {
      vi.mocked(useCaptchaEnabled).mockReturnValue(true)
      vi.mocked(useTurnstileSiteKey).mockReturnValue('site-key')
    }
  })
```

In `web/src/components/signup/__tests__/IdentitySheet.test.tsx`, add the same `vi.mock('#/lib/captcha', …)` block (enabled) after the turnstile mock — no new test needed there; the mock keeps its existing captcha cases meaningful.

- [ ] **Step 2: Run to verify failure**

Run: `cd web && bunx vitest run src/lib/__tests__/captcha.test.tsx src/components/auth/__tests__/TurnstileField.test.tsx src/components/booking/__tests__/BookingForm.test.tsx`
Expected: `captcha.test.tsx` fails to resolve `#/lib/captcha`; the BookingForm "submits without a captcha" case fails (`onSubmit` not called — the gate still demands a token).

- [ ] **Step 3: Implement the hook and the field**

Create `web/src/lib/captcha.ts`:

```ts
import { useRouter } from '@tanstack/react-router'
import type { PublicConfig } from '#/api/config'

/**
 * Reads `publicConfig.turnstileSiteKey` from the root route's `beforeLoad` context.
 *
 * Uses `useRouter({ warn: false })` rather than `useRouteContext` because the latter throws when
 * rendered outside a `<RouterProvider>` (e.g. in a component test that mounts a form on its own)
 * — `useRouter` degrades to `undefined` instead, so forms can render in isolation. The root
 * context is set once in `beforeLoad`, before any form mounts, so a plain (non-reactive) read of
 * `router.state` is safe. An empty string means the deployment has no Turnstile configured
 * (`GET /api/v1/config` omits `turnstileSiteKey` when the TURNSTILE_* pair is unset).
 */
export function useTurnstileSiteKey(): string {
  const router = useRouter({ warn: false })
  const rootContext = router?.state.matches[0]?.context as { publicConfig?: PublicConfig } | undefined
  return rootContext?.publicConfig?.turnstileSiteKey ?? ''
}

/**
 * Whether this deployment asks for a captcha at all. Every captcha gate in the app must be
 * `captchaEnabled && !captchaToken`: the Go side only verifies `X-Captcha-Token` when
 * `cfg.Capabilities.Turnstile` is on (internal/httpserver's RequireCaptchaIfAnon and
 * authCaptchaMiddleware), so a UI that demanded a token on an instance without keys would be
 * unusable for nothing (spec §8: an unset capability is invisible, never broken).
 */
export function useCaptchaEnabled(): boolean {
  return useTurnstileSiteKey() !== ''
}
```

Replace `web/src/components/auth/TurnstileField.tsx` with:

```tsx
import { Turnstile } from '@marsidev/react-turnstile'
import { useTurnstileSiteKey } from '#/lib/captcha'

/**
 * The Cloudflare Turnstile widget, or nothing at all when the deployment has no site key
 * (`useTurnstileSiteKey() === ''`): an empty sitekey makes Cloudflare's widget error out or never
 * load, and every consumer gates its submit on `useCaptchaEnabled()` from the same module, so the
 * two can never disagree about whether a token is expected.
 */
export function TurnstileField({ onToken }: { onToken: (token: string | null) => void }) {
  const siteKey = useTurnstileSiteKey()
  if (siteKey === '') return null

  return (
    <div data-slot="turnstile-field">
      <Turnstile
        siteKey={siteKey}
        onSuccess={onToken}
        onExpire={() => onToken(null)}
        onError={() => onToken(null)}
        // No `size` here on purpose: `@marsidev/react-turnstile` applies a fixed inline style to
        // this container based on `options.size` (e.g. `flexible` forces a 65px-tall, 300px-min
        // box) regardless of what the widget actually renders. Production's sitekey is configured
        // as an invisible widget that renders nothing, so that forced box used to sit as a dead
        // empty placeholder in the form. Leaving `size` unset means the container gets no inline
        // sizing at all — it collapses to nothing when the widget renders nothing, and still
        // sizes itself naturally around the dev/e2e test key's visible checkbox widget.
        options={{ theme: 'auto' }}
      />
    </div>
  )
}
```

- [ ] **Step 4: Update every gate**

Each file gets `import { useCaptchaEnabled } from '#/lib/captcha'` and a `const captchaEnabled = useCaptchaEnabled()` inside the component/hook body (next to its `captchaToken` state), then:

- `web/src/components/booking/BookingForm.tsx:104` — `if (!captchaToken) {` → `if (captchaEnabled && !captchaToken) {`, and line 117 `turnstileToken: captchaToken,` → `turnstileToken: captchaToken ?? undefined,`.
- `web/src/components/signup/IdentitySheet.tsx:71` — `if (needsCaptcha && !captchaToken) {` → `if (needsCaptcha && captchaEnabled && !captchaToken) {`.
- `web/src/components/poll/use-answer-draft.ts:95` — `const needsCaptcha = isGuest && !isEditing` → `const needsCaptcha = isGuest && !isEditing && captchaEnabled` (the hook is a React hook already; add the `useCaptchaEnabled()` call at its top, with the other `useState` calls — the existing gate at line 127 and `AnswerForm`'s `draft.needsCaptcha && <TurnstileField …/>` then follow automatically).
- `web/src/components/poll/Comments.tsx:73` — `if (isGuest && !captchaToken) {` → `if (isGuest && captchaEnabled && !captchaToken) {`.
- `web/src/routes/login.tsx:49` — `if (!captchaToken) {` → `if (captchaEnabled && !captchaToken) {` (Task 14 replaces this whole form; do the one-liner now so the tree is green).
- `web/src/routes/signup.tsx:220` — same one-liner.
- `web/src/routes/forgot-password.tsx:115` — `if (!captchaToken) return` → `if (captchaEnabled && !captchaToken) return`; line 162 `disabled={submitting || !captchaToken}` → `disabled={submitting || (captchaEnabled && !captchaToken)}`.

- [ ] **Step 5: Run the tests, typecheck, lint**

Run: `cd web && bunx vitest run && bun run typecheck && bun run lint`
Expected: all green (IdentitySheet's and BookingForm's existing "requires the captcha" cases still pass because their mocks say enabled).

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/captcha.ts web/src/lib/__tests__/captcha.test.tsx web/src/components web/src/routes
git commit -m "fix(web): captcha gates follow the Turnstile capability flag

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 14: Auth pages — verified gate in the SPA, verify-email token consumption, resend, locked message, OIDC button

**Files:**
- Create: `web/src/components/auth/CredentialLoginForm.tsx`, `web/src/components/auth/__tests__/CredentialLoginForm.test.tsx`, `web/src/components/auth/OidcButton.tsx`, `web/src/lib/session-guard.ts`
- Modify: `web/src/routes/login.tsx`, `web/src/routes/signup.tsx`, `web/src/routes/forgot-password.tsx`, `web/src/routes/verify-email.tsx`; route guards in `web/src/routes/dashboard.tsx`, `new.tsx`, `settings.tsx`, `bookings/index.tsx`, `bookings/new.tsx`, `bookings/$id/edit.tsx`, `bookings/$id/index.tsx`, `p/$id/edit.tsx`, `accept-invitation/$id.tsx`, `admin/route.tsx`; `web/messages/en.json`, `web/messages/nb.json`

**Interfaces:**
- Consumes: `signInWithCredential`, `signUpWithCredential`, `requestPasswordReset`, `me`, `signOut`, `verifyEmail`, `requestEmailVerification`, `oauthAuthorizeUrl` (Task 12); `useCaptchaEnabled` (Task 13); `publicConfig.oidcEnabled`/`oidcName` (already in `PublicConfig`).
- Produces: `requireVerifiedSession(context: { session: Session }, next: string): void` in `#/lib/session-guard`; `CredentialLoginForm({ onSignedIn })`; `OidcButton({ provider, name, next })`; `/verify-email?token=…` consumes the token.

- [ ] **Step 1: Messages**

Add to `web/messages/en.json` (next to the other `auth_*` keys) and `web/messages/nb.json`:

```json
  "auth_login_locked": "This account has been locked. Contact support if you think this is a mistake.",
  "auth_login_unverified_hint": "Check your inbox for the verification link, or send it again.",
  "auth_continue_with_sso": "Continue with {name}",
  "auth_verify_pending_title": "Verify your email",
  "auth_verify_pending_body": "We sent a verification link to {email}. Click it to start using whenweall.",
  "auth_verify_verifying": "Verifying your email…",
  "auth_verify_sign_out": "Sign out",
```

```json
  "auth_login_locked": "Denne kontoen er låst. Kontakt oss hvis du mener dette er en feil.",
  "auth_login_unverified_hint": "Sjekk innboksen for bekreftelseslenken, eller send den på nytt.",
  "auth_continue_with_sso": "Fortsett med {name}",
  "auth_verify_pending_title": "Bekreft e-posten din",
  "auth_verify_pending_body": "Vi har sendt en bekreftelseslenke til {email}. Klikk på den for å begynne å bruke whenweall.",
  "auth_verify_verifying": "Bekrefter e-posten din…",
  "auth_verify_sign_out": "Logg ut",
```

Also change `auth_verify_error_body` to `"This verification link is expired or invalid. Sign in and request a new one."` / `"Denne bekreftelseslenken er utløpt eller ugyldig. Logg inn og be om en ny."` (the old copy promised an email field that no longer exists). Keep `auth_login_unverified`, `auth_resend_verification`, `auth_verify_resent` — they come back into use here.

- [ ] **Step 2: Write the failing login-form test**

Create `web/src/components/auth/__tests__/CredentialLoginForm.test.tsx`:

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CredentialLoginForm } from '#/components/auth/CredentialLoginForm'
import { me, requestEmailVerification, signInWithCredential, signOut } from '#/api/auth'

vi.mock('#/api/auth', () => ({
  signInWithCredential: vi.fn(),
  me: vi.fn(),
  signOut: vi.fn(),
  requestEmailVerification: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
// Captcha off: the deployment under test has no Turnstile keys, so no token is ever demanded.
vi.mock('#/lib/captcha', () => ({
  useTurnstileSiteKey: () => '',
  useCaptchaEnabled: () => false,
}))

const verified = {
  id: '1', name: 'Ada', email: 'ada@example.com', emailVerified: true, locale: 'en' as const, hasPassword: true, isStaff: false,
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
  await user.type(screen.getByLabelText(/password/i), 'correct horse battery')
  await user.click(screen.getByRole('button', { name: /^sign in$/i }))
}

describe('CredentialLoginForm', () => {
  it('signs in without a captcha token when captcha is disabled and reports the verified user', async () => {
    vi.mocked(signInWithCredential).mockResolvedValue(verified)
    vi.mocked(me).mockResolvedValue(verified)
    const onSignedIn = vi.fn()
    const user = userEvent.setup()
    render(<CredentialLoginForm onSignedIn={onSignedIn} />)

    await fillAndSubmit(user)

    expect(signInWithCredential).toHaveBeenCalledExactlyOnceWith('ada@example.com', 'correct horse battery', null)
    expect(onSignedIn).toHaveBeenCalledExactlyOnceWith(verified)
  })

  it('shows the unverified card with a resend button instead of continuing', async () => {
    const unverified = { ...verified, emailVerified: false }
    vi.mocked(signInWithCredential).mockResolvedValue(unverified)
    vi.mocked(me).mockResolvedValue(unverified)
    vi.mocked(requestEmailVerification).mockResolvedValue(undefined)
    const onSignedIn = vi.fn()
    const user = userEvent.setup()
    render(<CredentialLoginForm onSignedIn={onSignedIn} />)

    await fillAndSubmit(user)

    expect(onSignedIn).not.toHaveBeenCalled()
    expect(await screen.findByText(/isn't verified yet/i)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /resend verification email/i }))
    expect(requestEmailVerification).toHaveBeenCalledOnce()
  })

  it('treats a sign-in whose session is immediately refused as a locked account', async () => {
    vi.mocked(signInWithCredential).mockResolvedValue(verified)
    vi.mocked(me).mockResolvedValue(null) // AuthMountGuard answered 403 "account is locked"
    vi.mocked(signOut).mockResolvedValue(undefined)
    const onSignedIn = vi.fn()
    const user = userEvent.setup()
    render(<CredentialLoginForm onSignedIn={onSignedIn} />)

    await fillAndSubmit(user)

    expect(signOut).toHaveBeenCalledOnce()
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(await screen.findByText(/has been locked/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 3: Run to verify failure**

Run: `cd web && bunx vitest run src/components/auth/__tests__/CredentialLoginForm.test.tsx`
Expected: FAIL — cannot resolve `#/components/auth/CredentialLoginForm`.

- [ ] **Step 4: The login form component and the OIDC button**

Create `web/src/components/auth/CredentialLoginForm.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import * as z from 'zod'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { authErrorMessage } from '#/lib/auth-errors'
import { useCaptchaEnabled } from '#/lib/captcha'
import { m } from '#/lib/i18n'
import { me, requestEmailVerification, signInWithCredential, signOut, type AuthUser } from '#/api/auth'

const emailSchema = z.email()

type Outcome = { kind: 'form' } | { kind: 'unverified' } | { kind: 'locked' }

/**
 * The email+password form on /login, router-free so it can be unit-tested. After Limen accepts
 * the credentials this re-reads `me()` to learn what kind of session it got:
 *
 *   - `null`: the session exists but internal/auth's AuthMountGuard refuses it — the account is
 *     locked (the only way a fresh sign-in yields no `/me`). Sign out again (allowed for a locked
 *     session) and say so.
 *   - `emailVerified === false`: the old Better-Auth 403 branch, re-expressed — show the
 *     unverified card with a resend button (the session is what authorizes the resend) rather
 *     than continuing into an app that would refuse every request.
 *   - otherwise: `onSignedIn(user)`; the route decides where to go.
 */
export function CredentialLoginForm({ onSignedIn }: { onSignedIn: (user: AuthUser) => void | Promise<void> }) {
  const captchaEnabled = useCaptchaEnabled()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [errors, setErrors] = useState<{ email?: string; password?: string }>({})
  const [submitting, setSubmitting] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>({ kind: 'form' })
  const [resending, setResending] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    const fieldErrors: typeof errors = {}
    if (!emailSchema.safeParse(email).success) fieldErrors.email = m.auth_error_email_invalid()
    if (password.length === 0) fieldErrors.password = m.auth_error_password_required()
    setErrors(fieldErrors)
    if (Object.keys(fieldErrors).length > 0) return
    if (captchaEnabled && !captchaToken) {
      toast.error(m.auth_error_captcha_required())
      return
    }

    setSubmitting(true)
    try {
      await signInWithCredential(email, password, captchaToken)
      const user = await me()
      if (!user) {
        await signOut()
        setOutcome({ kind: 'locked' })
        return
      }
      if (!user.emailVerified) {
        setOutcome({ kind: 'unverified' })
        return
      }
      await onSignedIn(user)
    } catch (error) {
      toast.error(authErrorMessage(error))
    } finally {
      setSubmitting(false)
    }
  }

  async function handleResend() {
    setResending(true)
    try {
      await requestEmailVerification()
      toast.success(m.auth_verify_resent())
    } catch (error) {
      toast.error(authErrorMessage(error))
    } finally {
      setResending(false)
    }
  }

  if (outcome.kind === 'unverified') {
    return (
      <div className="flex flex-col gap-3 rounded-lg border border-border bg-secondary/60 p-4 text-sm" role="status">
        <p className="font-medium">{m.auth_login_unverified()}</p>
        <p className="text-muted-foreground">{m.auth_login_unverified_hint()}</p>
        <Button type="button" variant="outline" size="sm" disabled={resending} onClick={() => void handleResend()}>
          {m.auth_resend_verification()}
        </Button>
      </div>
    )
  }

  if (outcome.kind === 'locked') {
    return (
      <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm" role="alert">
        <p>{m.auth_login_locked()}</p>
      </div>
    )
  }

  return (
    <form onSubmit={(e) => void handleSubmit(e)} noValidate className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="login-email">{m.auth_email_label()}</Label>
        <Input
          id="login-email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          aria-invalid={!!errors.email}
          aria-describedby={errors.email ? 'login-email-error' : undefined}
        />
        {errors.email && (
          <p id="login-email-error" className="text-sm text-destructive">
            {errors.email}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="login-password">{m.auth_password_label()}</Label>
        <Input
          id="login-password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-invalid={!!errors.password}
          aria-describedby={errors.password ? 'login-password-error' : undefined}
        />
        {errors.password && (
          <p id="login-password-error" className="text-sm text-destructive">
            {errors.password}
          </p>
        )}
      </div>

      <TurnstileField onToken={setCaptchaToken} />

      <Button type="submit" className="w-full" disabled={submitting}>
        {submitting ? m.auth_login_submitting() : m.auth_login_submit()}
      </Button>
    </form>
  )
}
```

Create `web/src/components/auth/OidcButton.tsx`:

```tsx
import { KeyRound } from 'lucide-react'
import { Button } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { safeNext } from '#/lib/search'
import { oauthAuthorizeUrl } from '#/api/auth'

/**
 * The bring-your-own-OIDC counterpart of `GoogleButton`: `provider` is `publicConfig.oidcName`
 * (the Go side mounts `/api/v1/auth/oauth/<OIDC_NAME>/authorize` — internal/auth/routes.txt),
 * `name` is the same value as the human-readable label. Only rendered when
 * `publicConfig.oidcEnabled` is true.
 */
export function OidcButton({ provider, name, next }: { provider: string; name: string; next: string }) {
  async function handleClick() {
    const url = await oauthAuthorizeUrl(provider, new URL(safeNext(next), window.location.origin).toString())
    window.location.href = url
  }

  return (
    <Button type="button" variant="outline" className="w-full gap-2" onClick={() => void handleClick()}>
      <KeyRound aria-hidden="true" className="size-4" />
      {m.auth_continue_with_sso({ name })}
    </Button>
  )
}
```

- [ ] **Step 5: The route guard**

Create `web/src/lib/session-guard.ts`:

```ts
import { redirect } from '@tanstack/react-router'
import type { Session } from '#/lib/use-session'

/**
 * The `beforeLoad` guard for every route that needs a usable account: signed out → /login (with
 * `next` back here); signed in but unverified → /verify-email, where the pending card offers a
 * resend. Mirrors the server: internal/auth's RequireSession answers 403 email_unverified for an
 * unverified session, so a route that skipped this would only get as far as its loader's error.
 */
export function requireVerifiedSession(context: { session: Session }, next: string): void {
  if (!context.session) {
    throw redirect({ to: '/login', search: { next } })
  }
  if (!context.session.user.emailVerified) {
    throw redirect({ to: '/verify-email', search: {} })
  }
}
```

Apply it — in each file add `import { requireVerifiedSession } from '#/lib/session-guard'`, drop `redirect` from the `@tanstack/react-router` import if nothing else in the file uses it, and replace the `beforeLoad`:

- `dashboard.tsx`: `beforeLoad: ({ context }) => requireVerifiedSession(context, '/dashboard'),`
- `new.tsx`: `beforeLoad: ({ context }) => requireVerifiedSession(context, '/new'),`
- `settings.tsx`: `beforeLoad: ({ context }) => requireVerifiedSession(context, '/settings'),`
- `bookings/index.tsx`: `beforeLoad: ({ context }) => requireVerifiedSession(context, '/bookings'),`
- `bookings/new.tsx`: `beforeLoad: ({ context }) => requireVerifiedSession(context, '/bookings/new'),`
- `bookings/$id/edit.tsx`: `beforeLoad: ({ context, params }) => requireVerifiedSession(context, `/bookings/${params.id}/edit`),`
- `bookings/$id/index.tsx`: `beforeLoad: ({ context, params }) => requireVerifiedSession(context, `/bookings/${params.id}`),`
- `p/$id/edit.tsx`: `beforeLoad: ({ context, params }) => requireVerifiedSession(context, `/p/${params.id}/edit`),`
- `accept-invitation/$id.tsx`: `beforeLoad: ({ context, params }) => requireVerifiedSession(context, `/accept-invitation/${params.id}`),`
- `admin/route.tsx`: `if (!context.session?.isStaff || !context.session.user.emailVerified) throw notFound()`

- [ ] **Step 6: The pages**

Replace `web/src/routes/login.tsx` with:

```tsx
import { createFileRoute, Link, redirect, useNavigate, useRouter } from '@tanstack/react-router'
import { AuthCard } from '#/components/auth/AuthCard'
import { CredentialLoginForm } from '#/components/auth/CredentialLoginForm'
import { GoogleButton } from '#/components/auth/GoogleButton'
import { OidcButton } from '#/components/auth/OidcButton'
import { Separator } from '#/components/ui/separator'
import { m } from '#/lib/i18n'
import { nextSearchSchema, safeNext } from '#/lib/search'

export const Route = createFileRoute('/login')({
  validateSearch: nextSearchSchema,
  beforeLoad: ({ context, search }) => {
    if (context.session) {
      // An unverified session has nowhere useful to go but the verify page.
      if (!context.session.user.emailVerified) throw redirect({ to: '/verify-email', search: {} })
      throw redirect({ href: safeNext(search.next, '/dashboard') })
    }
  },
  component: LoginPage,
})

function LoginPage() {
  const { next } = Route.useSearch()
  const { publicConfig } = Route.useRouteContext()
  const router = useRouter()
  const navigate = useNavigate()

  const showProviders = publicConfig.googleEnabled || publicConfig.oidcEnabled

  return (
    <AuthCard
      title={m.auth_login_title()}
      subtitle={m.auth_login_subtitle()}
      footer={
        <>
          <span>
            {m.auth_login_no_account()}{' '}
            <Link to="/signup" search={{ next }} className="font-medium text-primary-ink hover:underline">
              {m.auth_signup_link()}
            </Link>
          </span>
          <Link to="/forgot-password" className="text-primary-ink hover:underline">
            {m.auth_forgot_password_link()}
          </Link>
        </>
      }
    >
      <CredentialLoginForm
        onSignedIn={async () => {
          await router.invalidate()
          await navigate({ href: safeNext(next) })
        }}
      />

      {showProviders && (
        <>
          <div className="flex items-center gap-3">
            <Separator className="flex-1" />
            <span className="text-xs text-muted-foreground uppercase">{m.auth_or()}</span>
            <Separator className="flex-1" />
          </div>
          {publicConfig.googleEnabled && <GoogleButton next={safeNext(next)} />}
          {publicConfig.oidcEnabled && publicConfig.oidcName && (
            <OidcButton provider={publicConfig.oidcName} name={publicConfig.oidcName} next={safeNext(next)} />
          )}
        </>
      )}
    </AuthCard>
  )
}
```

In `web/src/routes/signup.tsx`:
- imports: add `import { OidcButton } from '#/components/auth/OidcButton'` and `import { useCaptchaEnabled } from '#/lib/captcha'` (the latter exists since Task 13).
- `handleSubmit`: the gate is already `captchaEnabled && !captchaToken`; replace the four-line comment + `await signUpWithCredential(email, password)` with:
  ```tsx
      // `name`/`locale` ride along in the body for internal/auth's signup hook; signup mints no
      // session, so the success card below tells the user to go verify.
      await signUpWithCredential(email, password, name.trim(), captchaToken)
  ```
- declare `const showProviders = publicConfig.googleEnabled || publicConfig.oidcEnabled` right after `const strength = passwordStrength(password)`, and replace the `{publicConfig.googleEnabled && ( … <GoogleButton next={safeNext(next)} /> … )}` block at the bottom of the form's `AuthCard` with:
  ```tsx
      {showProviders && (
        <>
          <div className="flex items-center gap-3">
            <Separator className="flex-1" />
            <span className="text-xs text-muted-foreground uppercase">{m.auth_or()}</span>
            <Separator className="flex-1" />
          </div>
          {publicConfig.googleEnabled && <GoogleButton next={safeNext(next)} />}
          {publicConfig.oidcEnabled && publicConfig.oidcName && (
            <OidcButton provider={publicConfig.oidcName} name={publicConfig.oidcName} next={safeNext(next)} />
          )}
        </>
      )}
  ```

In `web/src/routes/forgot-password.tsx`: `await requestPasswordReset(email)` → `await requestPasswordReset(email, captchaToken)` (the gate/disabled changes landed in Task 13).

Replace `web/src/routes/verify-email.tsx` with:

```tsx
import { useEffect, useRef, useState } from 'react'
import { createFileRoute, Link, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { Button, buttonVariants } from '#/components/ui/button'
import { authErrorMessage } from '#/lib/auth-errors'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { requestEmailVerification, signOut, verifyEmail } from '#/api/auth'
import { useSession } from '#/lib/use-session'

export const Route = createFileRoute('/verify-email')({
  // `token` is what the verify_email mail links to (internal/auth.enqueueTokenMail builds
  // `${APP_URL}/verify-email?token=<t>`). `done`/`error` are kept for old links. TanStack's
  // default search parser turns `?done=1` into a number, so accept either without transforming.
  validateSearch: z.object({
    token: z.string().optional(),
    done: z.union([z.string(), z.number()]).optional(),
    error: z.string().optional(),
  }),
  component: VerifyEmailPage,
})

/**
 * Four states, decided in order:
 *   1. `?token=` present → consume it via POST /api/v1/auth/verify-email (public route), then the
 *      done card. The session, if any, is refreshed so the router sees `emailVerified: true`.
 *   2. Signed in and verified (or a legacy `?done=1`) → the done card.
 *   3. Signed in, unverified (the route guard sends every gated page here) → the pending card:
 *      resend (POST /email-verifications — needs exactly this session) and sign out.
 *   4. Signed out, no token → the expired card with a link to sign in.
 */
function VerifyEmailPage() {
  const { token, done } = Route.useSearch()
  const session = useSession()

  if (token) return <VerifyWithToken token={token} />
  if (String(done) === '1' || session?.user.emailVerified) return <VerifyDone />
  if (session) return <VerifyPending email={session.user.email} />
  return <VerifyExpired />
}

function VerifyWithToken({ token }: { token: string }) {
  const router = useRouter()
  const [state, setState] = useState<'verifying' | 'done' | 'error'>('verifying')
  const started = useRef(false)

  useEffect(() => {
    // The token is single-use; StrictMode's double effect (and a re-render) must not spend it
    // twice — the second attempt would report "expired" for a link that just worked.
    if (started.current) return
    started.current = true
    verifyEmail(token)
      .then(async () => {
        await router.invalidate()
        setState('done')
      })
      .catch(() => setState('error'))
  }, [router, token])

  if (state === 'verifying') {
    return (
      <AuthCard title={m.auth_verify_pending_title()}>
        <p className="text-sm text-muted-foreground" role="status">
          {m.auth_verify_verifying()}
        </p>
      </AuthCard>
    )
  }
  if (state === 'done') return <VerifyDone />
  return <VerifyExpired />
}

function VerifyDone() {
  return (
    <AuthCard title={m.auth_verify_done_title()}>
      <p className="text-sm text-muted-foreground">{m.auth_verify_done_body()}</p>
      <Link to="/dashboard" className={cn(buttonVariants(), 'w-full')}>
        {m.auth_verify_done_cta()}
      </Link>
    </AuthCard>
  )
}

function VerifyPending({ email }: { email: string }) {
  const router = useRouter()
  const navigate = useNavigate()
  const [submitting, setSubmitting] = useState(false)

  async function handleResend() {
    setSubmitting(true)
    try {
      await requestEmailVerification()
      toast.success(m.auth_verify_resent())
    } catch (error) {
      toast.error(authErrorMessage(error))
    } finally {
      setSubmitting(false)
    }
  }

  async function handleSignOut() {
    await signOut()
    await router.invalidate()
    await navigate({ to: '/' })
  }

  return (
    <AuthCard title={m.auth_verify_pending_title()} subtitle={m.auth_verify_pending_body({ email })}>
      <Button type="button" className="w-full" disabled={submitting} onClick={() => void handleResend()}>
        {submitting ? m.auth_verify_resend_submitting() : m.auth_verify_resend_submit()}
      </Button>
      <Button type="button" variant="ghost" className="w-full" onClick={() => void handleSignOut()}>
        {m.auth_verify_sign_out()}
      </Button>
    </AuthCard>
  )
}

function VerifyExpired() {
  return (
    <AuthCard title={m.auth_verify_error_title()} subtitle={m.auth_verify_error_body()}>
      <Link to="/login" className={cn(buttonVariants(), 'w-full')}>
        {m.auth_login_link()}
      </Link>
    </AuthCard>
  )
}
```

- [ ] **Step 7: Run tests, typecheck, lint**

Run: `cd web && bunx vitest run && bun run typecheck && bun run lint`
Expected: all green, including the three `CredentialLoginForm` cases and `messages.test.ts` (both locale files gained the same keys).

- [ ] **Step 8: Commit**

```bash
git add web/src web/messages
git commit -m "feat(web): verify-email consumes the token; unverified/locked handling; OIDC button

Route guards send unverified sessions to /verify-email (resend + sign out);
the login form re-reads /me after sign-in to detect locked and unverified
accounts; the OIDC provider gets a button when configured.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 15: Settings — editable name, delete account, locale persisted for signed-in users

**Files:**
- Create: `web/src/components/auth/DeleteAccountDialog.tsx`, `web/src/components/auth/__tests__/DeleteAccountDialog.test.tsx`
- Modify: `web/src/routes/settings.tsx`, `web/src/components/layout/LocaleSwitcher.tsx`

**Interfaces:**
- Consumes: `updateProfile`, `deleteOwnAccount`, `AuthUser.hasPassword` (Task 12); existing `settings_name_*`, `settings_danger_*`, `settings_delete_*` message keys (they come back into use; `settings_delete_verify_sent` stays dead and is removed in Task 17).
- Produces: `DeleteAccountDialog({ hasPassword, onDelete })`; `LocaleSwitcher` PATCHes the locale before reloading when signed in.

- [ ] **Step 1: Write the failing dialog test**

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DeleteAccountDialog } from '#/components/auth/DeleteAccountDialog'

afterEach(() => cleanup())

describe('DeleteAccountDialog', () => {
  it('asks a credential account for its password and passes it to onDelete', async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(<DeleteAccountDialog hasPassword onDelete={onDelete} />)

    await user.click(screen.getByRole('button', { name: /^delete account$/i }))
    // Trigger and confirm share the label ("Delete account"); once the dialog is open the confirm
    // is the last matching button (Radix hides the page behind the modal from the a11y tree).
    const confirm = screen.getAllByRole('button', { name: /^delete account$/i }).at(-1)!
    expect(confirm).toBeDisabled()

    await user.type(screen.getByLabelText(/^password$/i), 'hunter2hunter2')
    expect(confirm).toBeEnabled()
    await user.click(confirm)

    expect(onDelete).toHaveBeenCalledExactlyOnceWith('hunter2hunter2')
  })

  it('needs no password for an OAuth-only account', async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(<DeleteAccountDialog hasPassword={false} onDelete={onDelete} />)

    await user.click(screen.getByRole('button', { name: /^delete account$/i }))
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument()
    await user.click(screen.getAllByRole('button', { name: /^delete account$/i }).at(-1)!)

    expect(onDelete).toHaveBeenCalledExactlyOnceWith(undefined)
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && bunx vitest run src/components/auth/__tests__/DeleteAccountDialog.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: The dialog**

Create `web/src/components/auth/DeleteAccountDialog.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '#/components/ui/dialog'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { m } from '#/lib/i18n'

/**
 * The settings page's danger zone (port of main:src/routes/settings.tsx's DangerZone). A
 * credential account confirms with its current password — DELETE /api/v1/me re-checks it
 * server-side (400 password_required / 403 invalid_password); an OAuth-only account has nothing
 * to re-enter and just confirms. Router-free: the route wires `onDelete` to the API call and the
 * post-deletion navigation.
 */
export function DeleteAccountDialog({
  hasPassword,
  onDelete,
}: {
  hasPassword: boolean
  onDelete: (password: string | undefined) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const [deleting, setDeleting] = useState(false)

  async function handleDelete(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setDeleting(true)
    try {
      await onDelete(hasPassword ? password : undefined)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-destructive/30 bg-destructive/5 p-4">
      <div>
        <h2 className="text-sm font-semibold text-destructive">{m.settings_danger_title()}</h2>
        <p className="text-sm text-muted-foreground">{m.settings_danger_subtitle()}</p>
      </div>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) setPassword('')
        }}
      >
        <DialogTrigger asChild>
          <Button type="button" variant="destructive" className="w-fit">
            {m.settings_delete_account()}
          </Button>
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{m.settings_delete_dialog_title()}</DialogTitle>
            <DialogDescription>
              {hasPassword ? m.settings_delete_dialog_body_password() : m.settings_delete_dialog_body_oauth()}
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={(e) => void handleDelete(e)} className="flex flex-col gap-4">
            {hasPassword && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="delete-password">{m.settings_delete_password_label()}</Label>
                <Input
                  id="delete-password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </div>
            )}
            <DialogFooter>
              <Button type="submit" variant="destructive" disabled={deleting || (hasPassword && !password)}>
                {deleting ? m.settings_delete_deleting() : m.settings_delete_confirm()}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </section>
  )
}
```

- [ ] **Step 4: The settings page**

Replace `web/src/routes/settings.tsx` with:

```tsx
import { useState, type FormEvent } from 'react'
import { createFileRoute, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import { DeleteAccountDialog } from '#/components/auth/DeleteAccountDialog'
import { HandleField } from '#/components/booking/HandleField'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Separator } from '#/components/ui/separator'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { requireVerifiedSession } from '#/lib/session-guard'
import { setHandle } from '#/api/bookings'
import { deleteOwnAccount, myOrgRoles, updateProfile } from '#/api/auth'

export const Route = createFileRoute('/settings')({
  beforeLoad: ({ context }) => requireVerifiedSession(context, '/settings'),
  // The caller's own membership roles in the active org — see HandleSection's doc comment for
  // why this gates the org-handle editor's visibility rather than just its submit handler.
  loader: () => myOrgRoles(),
  component: SettingsPage,
})

function SettingsPage() {
  const { session } = Route.useRouteContext()
  const orgRoles = Route.useLoaderData()
  const router = useRouter()
  const navigate = useNavigate()
  if (!session) return null

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-8 px-5 py-12 sm:py-16">
      <div>
        <h1 className="display text-3xl">{m.settings_title()}</h1>
      </div>

      <ProfileSection name={session.user.name} email={session.user.email} />

      <Separator />

      {session.org && orgRoles.includes('owner') && (
        <>
          <section>
            <HandleSection handle={session.org.slug} appUrl={window.location.origin} />
          </section>

          <Separator />
        </>
      )}

      <section className="flex flex-col gap-3">
        <div>
          <h2 className="text-sm font-semibold">{m.settings_language_title()}</h2>
          <p className="text-sm text-muted-foreground">{m.settings_language_subtitle()}</p>
        </div>
        <LocaleSwitcher />
      </section>

      <Separator />

      <DeleteAccountDialog
        hasPassword={session.user.hasPassword}
        onDelete={async (password) => {
          try {
            await deleteOwnAccount(password)
          } catch (error) {
            // invalid_password / password_required: the dialog stays open for another try.
            toast.error(
              errorCode(error) === 'invalid_password' || errorCode(error) === 'password_required'
                ? m.settings_delete_error()
                : m.auth_error_generic(),
            )
            return
          }
          toast.success(m.settings_delete_success())
          await router.invalidate()
          await navigate({ to: '/' })
        }}
      />
    </div>
  )
}

/** Editable display name (PATCH /api/v1/me); the email is read-only — it is the account's identity. */
function ProfileSection({ name, email }: { name: string; email: string }) {
  const router = useRouter()
  const [value, setValue] = useState(name)
  const [saving, setSaving] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = value.trim()
    if (!trimmed || trimmed.length > 80) return

    setSaving(true)
    try {
      await updateProfile({ name: trimmed })
      toast.success(m.settings_name_saved())
      await router.invalidate()
    } catch {
      toast.error(m.auth_error_generic())
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-sm font-semibold">{m.settings_profile_title()}</h2>
      <form onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="settings-name">{m.settings_name_label()}</Label>
          <Input id="settings-name" value={value} onChange={(e) => setValue(e.target.value)} maxLength={80} />
        </div>
        <Button type="submit" disabled={saving || !value.trim() || value.trim() === name}>
          {m.settings_name_save()}
        </Button>
      </form>
      <p className="text-sm text-muted-foreground">{email}</p>
    </section>
  )
}

/**
 * Wires `HandleField` (kept server-free so it can be unit-tested) to the Go `setHandle` API call,
 * refreshing the session afterwards so every booking link picks the new handle up.
 *
 * Only ever rendered for an org owner — `SettingsPage` gates it on `myOrgRoles()` including
 * `"owner"` — since `POST /api/v1/org/handle` is itself gated server-side by `RequireOwnerRole`
 * (internal/bookings/authz.go): a non-owner member used to see this same editable field and only
 * find out it wasn't theirs to change from a 403 toast after submitting.
 */
function HandleSection({ handle, appUrl }: { handle: string | null; appUrl: string }) {
  const router = useRouter()

  return (
    <HandleField
      currentHandle={handle}
      appUrl={appUrl}
      onSave={async (next) => {
        await setHandle(next)
        await router.invalidate()
      }}
    />
  )
}
```

- [ ] **Step 5: LocaleSwitcher persists the choice for a signed-in user**

Replace `web/src/components/layout/LocaleSwitcher.tsx` with:

```tsx
import { appConfig, type AppLocale } from '#/app.config'
import { getLocale, m, setLocale } from '#/lib/i18n'
import { useSession } from '#/lib/use-session'
import { cn } from '#/lib/utils'
import { updateProfile } from '#/api/auth'

const LOCALE_LABELS: Record<AppLocale, () => string> = {
  en: m.locale_en,
  nb: m.locale_nb,
}

export function LocaleSwitcher({ className }: { className?: string }) {
  const session = useSession()
  const activeLocale = getLocale()

  async function handleSelect(locale: AppLocale) {
    if (locale === activeLocale) return

    // Signed in: persist first (PATCH /api/v1/me writes user_preferences.locale, which every mail
    // to this user renders in), THEN switch the cookie — setLocale reloads the page, and a
    // fire-and-forget request would be aborted by that navigation. Works for unverified users
    // too (the route allows it), so the resent verification mail comes in the new language. A
    // failure is not worth blocking the UI switch over.
    if (session) {
      try {
        await updateProfile({ locale })
      } catch {
        // cookie-only switch still happens below
      }
    }
    // Sets the `whenweall_locale` cookie and reloads the page by default.
    setLocale(locale)
  }

  return (
    <div
      role="group"
      aria-label={m.locale_switch_label()}
      className={cn('inline-flex items-center gap-0.5 rounded-full border border-border/70 p-0.5', className)}
    >
      {appConfig.locales.map((locale) => (
        <button
          key={locale}
          type="button"
          aria-pressed={locale === activeLocale}
          onClick={() => void handleSelect(locale)}
          className={cn(
            'focus-ring rounded-full px-2.5 py-1 text-xs font-medium tracking-wide text-muted-foreground uppercase transition-colors',
            'hover:text-foreground aria-pressed:bg-secondary aria-pressed:text-foreground',
          )}
        >
          {LOCALE_LABELS[locale]()}
        </button>
      ))}
    </div>
  )
}
```

- [ ] **Step 6: Run tests, typecheck, lint**

Run: `cd web && bunx vitest run && bun run typecheck && bun run lint`
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add web/src
git commit -m "feat(web): settings name form and delete account; locale switch persists for users

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 16: Organization switcher in the user menu; accepting an invitation switches to the joined org

**Files:**
- Modify: `web/src/components/layout/UserMenu.tsx`, `web/src/components/auth/AcceptInvitationCard.tsx`, `web/src/components/auth/__tests__/AcceptInvitationCard.test.tsx`, `web/src/routes/accept-invitation/$id.tsx`, `web/messages/en.json`, `web/messages/nb.json`

**Interfaces:**
- Consumes: `listOrganizations`, `switchOrganization`, `acceptInvitation` (Task 12).
- Produces: `AcceptInvitationCard` calls `onAccepted(orgSlug: string | null)`; the user menu shows an "Organizations" submenu (only when the user has more than one) that switches the active org.

- [ ] **Step 1: Messages**

Add to both `web/messages/en.json` and `web/messages/nb.json`:

```json
  "nav_organizations": "Organizations",
  "nav_org_switched": "Switched to {name}.",
  "nav_org_switch_failed": "Couldn't switch organization. Try again.",
```

```json
  "nav_organizations": "Organisasjoner",
  "nav_org_switched": "Byttet til {name}.",
  "nav_org_switch_failed": "Kunne ikke bytte organisasjon. Prøv igjen.",
```

- [ ] **Step 2: Update the AcceptInvitationCard test first**

In `web/src/components/auth/__tests__/AcceptInvitationCard.test.tsx`, change the success case to:

```tsx
  it('calls acceptInvitation and hands the joined org slug to onAccepted', async () => {
    vi.mocked(acceptInvitation).mockResolvedValue({ orgSlug: 'team-ada' })
    const onAccepted = vi.fn()
    const user = userEvent.setup()
    render(<AcceptInvitationCard invitationId="inv_1" onAccepted={onAccepted} />)

    await user.click(screen.getByRole('button', { name: /accept invitation/i }))

    expect(acceptInvitation).toHaveBeenCalledExactlyOnceWith('inv_1')
    expect(onAccepted).toHaveBeenCalledExactlyOnceWith('team-ada')
  })
```

Run: `cd web && bunx vitest run src/components/auth/__tests__/AcceptInvitationCard.test.tsx`
Expected: FAIL — `onAccepted` called with no argument.

- [ ] **Step 3: Implement**

In `AcceptInvitationCard.tsx`, change the prop type to `onAccepted: (orgSlug: string | null) => void` and `handleAccept`'s body to:

```tsx
    try {
      const { orgSlug } = await acceptInvitation(invitationId)
      onAccepted(orgSlug)
    } catch {
      setFailed(true)
    } finally {
      setSubmitting(false)
    }
```

and update its doc comment's "switches the caller's active org" to "and the route switches the caller's active org to it".

Replace `web/src/routes/accept-invitation/$id.tsx` with:

```tsx
import { createFileRoute, useNavigate, useRouter } from '@tanstack/react-router'
import { AcceptInvitationCard } from '#/components/auth/AcceptInvitationCard'
import { requireVerifiedSession } from '#/lib/session-guard'
import { listOrganizations, switchOrganization } from '#/api/auth'

/**
 * Where the org_invite mail's CTA lands (internal/auth.enqueueInviteMail builds
 * `${APP_URL}/accept-invitation/<token>`). Signed out or unverified: bounce through /login or
 * /verify-email with `next` pointing back here, same as every other authenticated route. Signed
 * in: render a confirmation card (`AcceptInvitationCard`) — acceptance only fires on the card's
 * explicit button click, not on route hydration, since this URL is also hit by email link-safety
 * scanners and unfurlers.
 *
 * Limen's respond-to-invitation route inserts the member row but does NOT change the session's
 * active organization (unlike Better-Auth's acceptInvitation, which the old comment here relied
 * on), so after accepting we look the joined org up by slug and switch to it ourselves — otherwise
 * the user would land on their personal org's dashboard with the new org unreachable.
 */
export const Route = createFileRoute('/accept-invitation/$id')({
  beforeLoad: ({ context, params }) => requireVerifiedSession(context, `/accept-invitation/${params.id}`),
  component: AcceptInvitationPage,
})

function AcceptInvitationPage() {
  const { id } = Route.useParams()
  const router = useRouter()
  const navigate = useNavigate()

  async function handleAccepted(orgSlug: string | null) {
    if (orgSlug) {
      const orgs = await listOrganizations()
      const joined = orgs.find((org) => org.slug === orgSlug)
      if (joined && !joined.active) await switchOrganization(joined.id)
    }
    await router.invalidate()
    await navigate({ to: '/dashboard' })
  }

  return <AcceptInvitationCard invitationId={id} onAccepted={(orgSlug) => void handleAccepted(orgSlug)} />
}
```

Replace `web/src/components/layout/UserMenu.tsx` with:

```tsx
import { useState } from 'react'
import { Link, useNavigate, useRouter } from '@tanstack/react-router'
import { Building2, CalendarClock, LayoutDashboard, LogOut, Settings } from 'lucide-react'
import { toast } from 'sonner'
import { m } from '#/lib/i18n'
import { listOrganizations, signOut, switchOrganization, type OrgSummary } from '#/api/auth'
import type { Session } from '#/lib/use-session'
import { buttonVariants } from '#/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '#/components/ui/dropdown-menu'
import { cn } from '#/lib/utils'

function initial(name: string | null, email: string): string {
  const source = name?.trim() || email
  return source.slice(0, 1).toUpperCase()
}

export function UserMenu({ session }: { session: Session }) {
  const router = useRouter()
  const navigate = useNavigate()
  // Loaded when the menu opens, not on every page render: the header is on every page and most
  // users have exactly one organization (their personal one), for which no switcher is shown.
  const [orgs, setOrgs] = useState<OrgSummary[] | null>(null)

  if (!session) {
    return (
      <Link to="/login" className={cn(buttonVariants({ variant: 'ghost', size: 'sm' }))}>
        {m.nav_sign_in()}
      </Link>
    )
  }

  const { user } = session

  async function handleSignOut() {
    await signOut()
    await router.invalidate()
    await navigate({ to: '/' })
  }

  async function loadOrgs() {
    if (!user.emailVerified) return // GET /api/v1/me/organizations is verified-only
    try {
      setOrgs(await listOrganizations())
    } catch {
      setOrgs([])
    }
  }

  async function handleSwitch(orgId: string) {
    const target = orgs?.find((org) => org.id === orgId)
    if (!target || target.active) return
    try {
      await switchOrganization(orgId)
      await router.invalidate()
      toast.success(m.nav_org_switched({ name: target.name }))
    } catch {
      toast.error(m.nav_org_switch_failed())
    }
  }

  const activeOrg = orgs?.find((org) => org.active)

  return (
    <DropdownMenu onOpenChange={(open) => open && void loadOrgs()}>
      <DropdownMenuTrigger
        aria-label={m.nav_account_menu()}
        className={cn(
          'focus-ring inline-flex size-9 items-center justify-center rounded-full bg-accent-soft text-sm font-semibold text-accent-foreground',
          'transition-transform duration-200 hover:scale-105 data-[state=open]:scale-105',
        )}
      >
        {initial(user.name, user.email)}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="font-normal">
          <span className="block truncate text-sm font-medium">{user.name || user.email}</span>
          <span className="block truncate text-xs text-muted-foreground">{user.email}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/dashboard">
            <LayoutDashboard aria-hidden="true" />
            {m.nav_dashboard()}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to="/bookings">
            <CalendarClock aria-hidden="true" />
            {m.nav_booking_pages()}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to="/settings">
            <Settings aria-hidden="true" />
            {m.nav_settings()}
          </Link>
        </DropdownMenuItem>
        {orgs && orgs.length > 1 && (
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <Building2 aria-hidden="true" />
              <span className="truncate">{activeOrg?.name ?? m.nav_organizations()}</span>
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              <DropdownMenuLabel>{m.nav_organizations()}</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={activeOrg?.id} onValueChange={(id) => void handleSwitch(id)}>
                {orgs.map((org) => (
                  <DropdownMenuRadioItem key={org.id} value={org.id}>
                    <span className="truncate">{org.name}</span>
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void handleSignOut()}>
          <LogOut aria-hidden="true" />
          {m.nav_sign_out()}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
```

(No component test for the switcher itself: Radix submenus need a real pointer environment; the behaviour is pinned by `auth.test.ts` (API layer) and the Go `TestMyOrganizations_ListAndSwitch`/`TestInvitationFlow`.)

- [ ] **Step 4: Run tests, typecheck, lint**

Run: `cd web && bunx vitest run && bun run typecheck && bun run lint`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add web/src web/messages
git commit -m "feat(web): organization switcher; accepting an invitation switches to the joined org

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 17: Dead message keys, stale comments, README truth

**Files:**
- Modify: `web/messages/en.json`, `web/messages/nb.json`, `README.md`, `internal/httpserver/testroutes.go` (only if a stale phrase survives Task 5), `web/src/routes/signup.tsx` (comment)

**Interfaces:** none new.

- [ ] **Step 1: Delete the keys nothing brings back**

Of the 34 orphaned keys the audit listed, this plan revived every `settings_name_*`, `settings_danger_*`, `settings_delete_*` (except one), `auth_login_unverified`, `auth_resend_verification` and `auth_verify_resent`. Remove exactly these three from BOTH `en.json` and `nb.json`:

- `settings_delete_verify_sent` (Better-Auth's delete-by-email confirmation; `DELETE /api/v1/me` deletes immediately)
- `notif_channel_push` (web push is a stated non-goal)
- `admin_badge_banned` (the console shows `admin_badge_locked`)

Then confirm nothing references them and no other `auth_*`/`settings_*`/`nav_*` key added by this plan is unused:

Run: `cd web && for k in settings_delete_verify_sent notif_channel_push admin_badge_banned auth_login_locked auth_login_unverified_hint auth_continue_with_sso auth_verify_pending_title auth_verify_pending_body auth_verify_verifying auth_verify_sign_out nav_organizations nav_org_switched nav_org_switch_failed settings_name_saved settings_delete_error auth_login_unverified auth_resend_verification auth_verify_resent; do printf '%s: ' $k; grep -rl "m\.$k\b" src | wc -l; done`
Expected: the first three print `0` (and are gone from the json), every other key prints `1` or more.

- [ ] **Step 2: README**

- Line 189 (`- **Turnstile** (optional) on guest voting, commenting and booking, plus a per-IP rate`) → `- **Turnstile** (optional) on sign-in, sign-up, password reset, guest voting, commenting and booking, plus a per-IP rate`.
- Lines 296-297 (`TURNSTILE_SITE_KEY`/`TURNSTILE_SECRET_KEY` rows): change "Optional captcha on guest voting/commenting/booking." to "Optional captcha on sign-in/sign-up/password reset and guest voting/commenting/booking." and "Without this pair, public endpoints have no captcha" to "Without this pair, no endpoint asks for a captcha (the UI hides the widget)".
- Line 134 (`- **Sign in** with e-mail + password (verified), Google, or an external OIDC provider.`) → `- **Sign in** with e-mail + password (the address must be verified before the account can be used), Google, or an external OIDC provider (which must assert verified e-mails).`
- In the "Development" or wherever the locale switch is described (~line 511), append one sentence: "A signed-in user's choice is also stored server-side (`user_preferences.locale`) and used for every e-mail sent to them; guest forms send the visitor's locale along with the vote/claim/booking."

- [ ] **Step 3: Stale comments**

- `web/src/routes/signup.tsx`: make sure no remnant of the "`name` has nowhere to go server-side yet" comment survives (Task 14 replaced it).
- `web/src/api/auth.ts`: the `requestEmailVerification` doc comment's "that gap is a known limitation of this port (see the task report)" → replace with "The login form and the /verify-email pending card both call this with the fresh (unverified) session Limen minted at sign-in — internal/auth's AuthMountGuard allows exactly this route for an unverified session."
- `internal/httpserver/testroutes.go`: grep for `autoSignInOnSignUp` — the Task 5 rewrite removed every mention; if one survived, delete it.

Run: `grep -rn "autoSignInOnSignUp\|nowhere to go\|known limitation of this port" internal web/src | grep -v _test`
Expected: no output.

- [ ] **Step 4: Full gates**

Run: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./... && (cd web && bun run typecheck && bun run lint && bunx vitest run)`
Expected: everything green.

- [ ] **Step 5: Commit**

```bash
git add web/messages README.md web/src/api/auth.ts web/src/routes/signup.tsx internal/httpserver/testroutes.go
git commit -m "chore: drop dead message keys; README reflects verification gate and auth captcha

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

## Self-review (done while writing; recorded for the executor)

**Spec/contract coverage → task:** verify link consumed (14); gate + resend + auto-sign-in off + OAuth-verified reasoning (5, facts table); captcha capability in the SPA incl. login + BookingForm tests (13, 14); server captcha + client sends token (9, 12, 14); display name end to end (3, 4, 12, 15); self-service deletion + shared cascade + privacy copy stays true (6, 7, 15); OIDC button (14); invitation switches org + switcher + stale comment + Go invitation test (16, 11); locked user → `me()` null + login message (12, 14); Limen limiter key + `/me` exemption (8); OIDC verified-email guard + README (10); org slug hooks + slug cap (10); locale foundation: migration, `LocaleFor`, PATCH `/me` locale, LocaleSwitcher persists, mails carry `Data.Locale`, signup locale from body/cookie/Accept-Language, `mailer/format.go` with tests (1, 2, 3, 4, 7, 15); guest locale plumbing (12); verification/reset token consumption tests (5; reset already pinned by `TestPasswordResetEnqueuesMail`); orphaned keys (17). Contract interface names checked against every use: `Profile`, `GetProfile`, `SetProfile`, `LocaleFor`, `DeleteOwnAccount`, `CascadeDeleteUser`, `Session.EmailVerified`, `SupportedLocales`, `FormatDateTime`/`FormatDate`/`FormatTimeRange`, `useCaptchaEnabled`, `updateProfile`, `deleteOwnAccount`, `switchOrganization`, `listOrganizations`.

**Deliberate deviations from the contract, with reasons:** `user_preferences.user_id` is `bigint` (FK type must match `users.id`); the unverified exemption list additionally includes the four session-less credential routes (a stale unverified cookie must not block signing in as someone else); `switchOrganization`/`listOrganizations` go through our own `/api/v1/me/*` routes because Limen serializes organizations without ids; `acceptInvitation` returns `{ orgSlug }` (Limen's respond route does not switch or return the org id) and the accept route resolves slug → id via `listOrganizations()`.

**Type consistency:** `SwitchOrganization(w, r, orgID string) error` (Task 6) is what Task 7's handler and Task 6's `/probe/switch` call; `ListUserOrganizations(ctx, *Session)` is used by Tasks 7 and 11; `RequireSessionAllowUnverified` (Task 5) is used by Task 7; `MarkEmailVerified(ctx, email)` (Task 3) is used by Tasks 5, 6, 7; `verifiedEmailUserInfo(providers, guardedProvider)` matches its test; `OrgSummary` JSON tags match the TS `OrgSummary`; `AuthUser.hasPassword` is produced by Task 4's transformer, read by Task 12's `toAuthUser`, consumed by Task 15.

**Known follow-ups outside this plan:** Plans C/D must replace their hard-coded `Locale: "en"` for user recipients with `authSvc.LocaleFor(...)` and their English-only date strings with `mailer.FormatDateTime`; Plan B's `00010_drop_two_factor.sql` must also delete the `two_factors` line in `internal/auth/cascade.go`; Plan F adds the Mailpit-driven e2e for the verify link, the captcha-off flows and the invitation journey.
