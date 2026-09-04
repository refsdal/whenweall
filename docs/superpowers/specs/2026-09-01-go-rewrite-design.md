# whenweall v5 — Go rewrite design

**Date:** 2026-09-01
**Status:** approved design, pre-implementation

## Summary

whenweall re-platforms off Cloudflare Workers onto a single static **Go binary** that serves an
embedded React SPA, packaged as a hardened `FROM scratch` container with **Postgres as the only
other service**. Stripe and the entire billing/entitlements layer are removed — every user and
organization gets one implicit, unlimited plan. Mail moves to **SMTP** (required). Realtime rooms
are rebuilt as an in-process websocket hub with Postgres LISTEN/NOTIFY fan-out. The product is
positioned as free, open-source, and maximally self-hostable.

This supersedes the in-flight Bun + Docker migration (branch `feat/docker-bun`). That work is
abandoned as a runtime, but its design decisions carry over as constraints here: Postgres-only
(no Redis), SMTP required at boot, config validated loudly at boot, capability flags for optional
features, the compose.yaml shape, and the squashed fresh-baseline approach (there is no live user
data to migrate).

## Decisions made during design (do not re-litigate)

| Decision | Choice |
| --- | --- |
| Bun migration | Abandoned; full pivot to Go. Its designs are inputs, not code to keep. |
| Auth library | [Limen](https://limenauth.dev/) (`github.com/thecodearcher/limen`), **its features only**: email+password, OAuth (Google), generic OIDC, magic links, TOTP 2FA, sessions. **Passkeys are dropped** (Limen has no WebAuthn). *Amended 2026-09-01 after source review:* orgs/invitations use **Limen's `organization` plugin** (the website undersold it — the plugin covers members, roles, invitations with email callback, and active-org-in-session), not our own domain code. |
| Realtime | Own hub: `coder/websocket` + Postgres LISTEN/NOTIFY + presence table. No Centrifuge. |
| SSR | Gone entirely. Pure SPA; no prerendering, no OG-tag injection. Accepted trade-off. |
| Billing | Deleted wholesale, including the entitlements layer. Everything unlimited for everyone. Abuse control = rate limiting only. |
| Kept features | Google Calendar sync, Turnstile captcha, admin console, landing-page live stats — all capability-flagged as today. |
| Go stack | Stdlib-first: `net/http` (1.22+ routing, no framework), pgx via `database/sql`, sqlc, goose, `go:embed`, `wneessen/go-mail`, testcontainers-go. No GORM, no Echo/Gin. |
| Binary size | `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`. **No upx** (scanner false-positives, defeats page sharing). Target ~20 MB image all-in. |

## 1. Architecture & repo layout

One process, one binary, one port. The Go binary serves the JSON API under `/api/`, websockets
under `/api/.../ws`, and the embedded SPA for every other path (SPA fallback to `index.html`;
hashed assets get immutable cache headers). `compose.yaml` keeps its current shape (app + db,
Postgres 18, healthchecks) minus Stripe vars; `BETTER_AUTH_SECRET` renames to `AUTH_SECRET`.

Go moves in at the repo root; the React app moves to `web/`:

```
cmd/whenweall/main.go        # subcommands: serve (default), migrate, create-staff-user, healthcheck
internal/
  config/                    # env → validated Config struct (port of src/config.ts); boot fails loudly
  httpserver/                # router, middleware, SPA handler, embedded web/dist
  auth/                      # Limen wiring behind our own interface + orgs/invitations domain code
  polls/  bookings/  admin/  # domain packages (ports of src/server/*)
  rooms/                     # websocket hub, LISTEN/NOTIFY fan-out, presence, room_events
  jobs/                      # scheduled_jobs poller (FOR UPDATE SKIP LOCKED)
  mailer/                    # go-mail SMTP client, queue, html/template emails, .ics writer
  db/                        # pool, sqlc-generated queries, embedded goose migrations
web/                         # React SPA: Vite + TanStack Router (Start removed), paraglide, vitest
migrations/                  # goose SQL files (fresh baseline, see §6)
e2e/                         # Playwright, unchanged location; runs against the Go binary
```

Build flow: `vite build` in `web/` → `internal/httpserver/dist/` → `go:embed` → one static
binary. Local dev: Vite dev server proxies `/api` to `go run ./cmd/whenweall`.

Deleted: wrangler/workers config and types, `src/do/`, `@cloudflare/*`, `better-auth` +
plugins, `stripe`, `src/server/billing/`, drizzle + drizzle-kit, vitest workers project,
`spike/`, React Email templates (re-authored in Go).

## 2. API & frontend conversion

REST-ish JSON under `/api/v1/`, mapping ~1:1 from the existing `createServerFn` surface
(~30 endpoints: polls, votes, comments, claims, finalize, booking pages, bookings, dashboard,
admin, config capability flags). `GET /healthz` for liveness (checks DB). Auth routes are
whatever Limen mounts plus our org/invitation endpoints.

- Error envelope: `{"error": {"code": "...", "message": "..."}}`; `code` is machine-readable so
  the frontend maps to paraglide translations (i18n stays frontend-only).
- Validation in Go: hand-rolled `Validate()` methods per request type (no reflection/tag
  library). Zod schemas survive on the frontend for form UX — duplication is intentional.
- Frontend: TanStack Start → plain Vite + TanStack Router. Server-function imports become typed
  `fetch` wrappers in `web/src/api/` with hand-written TS types (no OpenAPI codegen for v1).
  better-auth React client → small hooks over Limen's session endpoints. Billing components and
  routes deleted.
- CSRF: `SameSite=Lax` session cookie + `Origin` check on mutating requests. No token dance.

## 3. Auth & organizations

- **Limen plugins enabled:** email+password (verification + password reset via SMTP), Google
  OAuth, generic OIDC (`OIDC_ISSUER/CLIENT_ID/SECRET` — bring-your-own Authentik/Keycloak),
  magic links, TOTP 2FA. All but email+password capability-flagged. Storage: Limen's
  `database/sql` adapter on the shared pool. Limen's session cookie authenticates everything.
- **Seam:** all Limen usage goes through `internal/auth`'s own interface (`CurrentUser`,
  `RequireSession`, `RequireStaff`). No handler imports Limen types. Limen is young; it must be
  swappable without touching call sites. Pin its version.
- **Orgs via Limen's `organization` plugin** (amended after source review): it provides
  organizations, members, configurable roles (owner/admin/member), invitations with expiring
  tokens, active-org-in-session, and mounts its own HTTP routes under the auth base path.
  Invitation emails route through our mail queue via `organization.WithSendInvitationMail`.
  Personal org auto-created on signup via a Limen `WithHTTPHooks` after-hook on the
  signup/OAuth-callback routes calling `organization.Use(a).CreateOrganization`. The accept
  flow keeps `/accept-invitation/{id}` in the SPA, backed by the plugin's
  `GET /invitations/token/{token}` + `POST /invitations/respond` endpoints.
- **Limen mechanics pinned by source review:** `Config.Secret` must be exactly 32 bytes — we
  derive it as `sha256(AUTH_SECRET)`. Verification/reset/magic-link/invitation emails are
  callbacks (`WithSendEmailVerificationMail`, `WithSendPasswordResetEmail`,
  `WithSendMagicLink`, `WithSendInvitationMail`) that enqueue into our mail queue. Limen's
  tables enter the goose baseline via its CLI migration generator
  (`limen generate migrations --driver postgres`), run once at development time.
- **Staff flag stays ours:** a `staff_users(user_id)` table rather than extending Limen's user
  schema; `RequireStaff` consults it.
- **Staff:** platform role on the user row; `RequireStaff` gates `/api/v1/admin/*`. Bootstrap via
  `whenweall create-staff-user --email ...` subcommand (works via `docker compose exec`).
- **Guests:** anonymous participant tokens (`claim-auth.ts`/`comment-auth.ts` logic) port as
  HMAC-signed tokens using `AUTH_SECRET`.
- **Rate limiting:** Postgres UNLOGGED-table fixed-window limiter wraps login, signup, magic
  link, and password reset.
- No auth-table data migration exists or is needed; Limen + org tables go into the fresh baseline.

## 4. Realtime rooms

In-process hub per room key (`poll:{id}`, `booking:{pageId}`, `stats:global`) using
`coder/websocket`; Postgres is the cross-replica bus.

- **Events:** domain code writes to `room_events` (`room_key`, per-room monotonic `seq`, JSON
  payload) **in the same transaction as the domain write**, then `NOTIFY` with
  `{room_key}:{seq}` after commit. Each replica holds one dedicated LISTEN connection (outside
  the pool), fetches the row, fans out locally. Notification carries a pointer, never a payload
  (no 8 KB NOTIFY cliff).
- **Catch-up:** client sends last-seen `seq` on (re)connect; server replays newer rows before
  going live. On a seq gap (pruned history) the client refetches full state. (Lesson from
  `fix/booking-live-catchup`.)
- **Presence:** PG table (`room_key`, `connection_id`, `replica_id`, `expires_at`) with
  heartbeat refresh; joins/leaves are ordinary room events; boot sweep + pruning job clear dead
  replicas' rows.
- **Backpressure:** bounded per-connection send queues; slow clients get disconnected. (Lesson
  from `fix/ws-unbounded-do`.)
- **Auth at upgrade:** session cookie or guest token, checked before upgrade.
- **Atomicity:** DO `#serialize()` guarantees become `SELECT ... FOR UPDATE` transactions for
  sign-up claims and booking slots. Overbooking must be impossible; dedicated concurrency tests
  pin this (§9).
- **Stats room:** counters aggregate in PG; hub throttles broadcasts (≥ a few seconds apart).
- `room_events` rows older than ~1 hour are pruned by a scheduled job.

## 5. Jobs & mail

- `scheduled_jobs` table: `id`, `type`, `payload` jsonb, `run_at`, `attempts`, `locked_by`,
  `locked_until`, `last_error`. One poller goroutine per replica claims due jobs with
  `FOR UPDATE SKIP LOCKED`. Exponential backoff; after 10 attempts a job parks as `failed` and
  **surfaces in the admin console** (lesson from `fix/mail-failure-visibility`).
- Job types: mail delivery, booking reminders, finalize/claim notification emails,
  `room_events` pruning, presence sweeps.
- **All mail is queued** — no inline sends from request handlers.
- SMTP client: `wneessen/go-mail` (stdlib `net/smtp` is frozen). Config as in compose.yaml:
  `SMTP_HOST` required at boot; `SMTP_PORT/USER/PASSWORD/SECURE`, `EMAIL_FROM`. No boot-time
  dial check — SMTP outages must not stop the app; failures surface in the queue.
- Templates: Go `html/template`, one shared layout, plain-text alternative part for every mail
  (new). `.ics` generation is a small pure-Go writer (port of `ics.ts`). Email localization
  keeps parity with today: if current templates pull paraglide messages, Go templates get a
  per-locale strings map fed from `messages/`; otherwise they stay English-only.

## 6. Data layer & migrations

- One pool: `database/sql` + pgx stdlib driver, shared by Limen, sqlc queries, and the jobs
  poller. The LISTEN connection is separate.
- **sqlc** for typed queries; hand-built SQL only where genuinely dynamic (admin search filters).
- **Baseline re-cut:** the D1-squashed schema's SQLite habits are fixed — `timestamptz` for
  times, `jsonb` for JSON columns (`availability`, `date_overrides`, `metadata`, payloads),
  real FKs. Billing and better-auth tables out; Limen + org tables in. Free because there is no
  live data.
- **goose** migrations, embedded via `go:embed`, auto-run at boot under a PG advisory lock.
  `MIGRATE_ON_BOOT=false` escape hatch + `whenweall migrate` subcommand.
- Source of truth: goose SQL files; sqlc reads the same schema, so types can't drift.

## 7. Container & hardening

Three-stage Dockerfile:

1. `oven/bun` — `vite build` → `web/dist`
2. `golang:1.25-alpine` (Limen requires Go ≥1.25) — `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=..."`;
   tz data via pure-Go `time/tzdata` import
3. `FROM scratch` — binary + `ca-certificates.crt` only (SMTP TLS, Google APIs).
   `USER 65532:65532`, `EXPOSE 3000`, `ENTRYPOINT ["/whenweall"]`

- No shell in the image → healthcheck is `["CMD", "/whenweall", "healthcheck"]` (hits own
  `/healthz`, exits 0/1).
- compose.yaml hardening, valid because the app writes zero bytes to disk:
  `read_only: true`, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`. No volumes,
  no tmpfs. The container is a pure function of its env vars.
- Releases: GitHub Actions, multi-arch (amd64 + arm64), pushed to `ghcr.io/refsdal/whenweall`
  (`latest` + semver), with buildx SBOM + provenance attestation.

## 8. README & self-host story

Rewritten around: **one ~20 MB container, one Postgres, nothing else.**

- Hero keeps the product pitch; platform story inverts to single-static-binary, no Cloudflare,
  no Node, no Redis, no telemetry. Workers badge replaced by Go/Docker badges.
- Five-minute quickstart at the top: `compose.yaml` + `.env` (set `AUTH_SECRET` and SMTP),
  `docker compose up -d`. SMTP framed honestly as the one prerequisite, with Mailpit pointer
  for trials.
- Full env-var reference table with capability-flag semantics (unset = feature invisible,
  never broken).
- Reverse proxy: two-line Caddyfile as the blessed path; nginx/Traefik websocket note.
- Ops story in two sentences: backup = `pg_dump`; upgrade = pull new tag, migrations run
  themselves.
- Admin bootstrap: `docker compose exec app /whenweall create-staff-user --email ...`;
  `docs/` runbook updated.
- Development section + CONTRIBUTING.md: `docker compose up db`, `go run ./cmd/whenweall`,
  `cd web && bun dev`.
- Stated non-goals: no Helm chart yet, no SQLite mode, no built-in TLS (bring a proxy).

## 9. Testing

- **Playwright e2e is the oracle.** Runtime-agnostic; carries over minus billing and passkey
  specs. Runs in CI against the real built Docker image + Postgres — proves the port and keeps
  the hardening claims honest.
- **Go tests against live Postgres via testcontainers-go.** The harness starts one PG
  container, migrates a template database once, clones it per test package
  (`CREATE DATABASE ... TEMPLATE ...`). `go test ./...` works anywhere Docker runs — laptop and
  CI identically. No mocked DB. The old `*.workers.test.ts` assertions are re-expressed in the
  Go ports they covered.
- **Concurrency tests** (the "must be impossible" bug): N goroutines racing the last sign-up
  slot / same booking slot; exactly one winner. Pins the `FOR UPDATE` transactions that replaced
  DO serialization.
- Frontend: existing Vitest unit project stays in `web/`; workers project deleted.
- CI: `go vet` + `golangci-lint` + `go test` (testcontainers) · web typecheck + lint + vitest ·
  Docker build · Playwright against the image.
- Implementation follows TDD.

## Amendment (2026-09-01, post-plan-1): baseline deltas vs §4/§5

The frozen goose baseline ports the Bun-branch DDL (the proven behavioral reference) rather than
this spec's sketches. Three deliberate deltas, ruled during plan 1's final review:

- **`room_events` has a global `bigserial id`, not a per-room `seq`.** The id is the client
  cursor. Known hazard plan 6 Task 1 MUST resolve before building catch-up: bigserial values are
  allocated before commit, so a lower-id event can become visible after a higher-id one was
  delivered, and a client resuming from `id > lastSeen` would miss it. Resolve via a visibility
  watermark (replay from `min(in-flight)`), or by assigning ids under the `room_state` row lock.
- **`scheduled_jobs` keeps `kind`/`locked_at`/`max_attempts` (default 5) and no status column** —
  §5's `type`/`locked_until`/`failed` wording is superseded; dead-letter = `attempts >=
  max_attempts`, lock expiry = `locked_at` + 5-minute timeout in code, mail jobs pass
  `max_attempts: 10` explicitly.
- **`ws_presence` keeps per-replica counts** (`room_key`, `replica_id`, `count`, `heartbeat_at`),
  not §4's per-connection rows. Presence totals are sums over replicas.

## Amendment (2026-09-03, completion pass): what shipped differently

Recorded after the parity audit of PR #67 so this spec describes the code it approved. Each item
supersedes the earlier text it names; the completion plans
`docs/superpowers/plans/2026-09-03-complete-0*.md` implement them.

- **Magic links and TOTP 2FA are not mounted** (decisions table, "Auth library" row; §3 bullets 1
  and "Rate limiting"). Commit 72a8306 removed Limen's `magic-link` and `two-factor` plugins:
  neither ever had a UI, and magic-link auto-created an account on first verify
  (`autoCreateUser` default), bypassing the credential sign-up's validation on every deployment
  that mounted it. Limen features in use: email+password (verification, password reset), Google
  OAuth, generic OIDC, sessions, organizations. `WithSendMagicLink` is not wired; the Postgres
  rate limiter wraps sign-in, sign-up and password-reset requests only. Migration
  `00010_drop_two_factor.sql` removes the plugin's leftover schema (`two_factors`,
  `users.two_factor_enabled`).
- **Email verification gates the app** (§3 bullet 1 implied it; the first cut lost it), but the
  gate has more shape than "every route 403s" — three exceptions, all deliberate
  (`internal/auth/session.go`, `internal/httpserver/domainauth.go`). Under the `/api/v1/auth/`
  mount, `AuthMountGuard` lets an unverified session through to `GET /api/v1/auth/me`,
  `POST .../verify-email`, `POST .../email-verifications`, `POST .../signout`, and the four
  session-less credential routes (`signin/credential`, `signup/credential`,
  `passwords/request-reset`, `passwords/reset`) — everything else under the mount
  (organizations, invitations, OAuth linking, password change) answers 403 `email_unverified`.
  Outside the mount, this application's own `RequireSession`/`RequireStaff` apply the same gate,
  except `PATCH`/`DELETE /api/v1/me` (`RequireSessionAllowUnverified`), which an unverified
  account must be able to reach to update its profile or delete itself. And the public poll/
  sign-up/booking routes don't 403 an unverified session at all: `viewerFromRequest` and
  `RequireCaptchaIfAnon` treat it exactly like no session, attributing a vote, claim, comment or
  booking to an anonymous guest rather than the unverified account. `GET /api/v1/config` needs no
  session in the first place — it is a public route, not a verification exemption. The SPA shows
  the "verify your address" card with a resend button. OAuth-created users count as verified.
- **Per-user locale is persisted** in `user_preferences(user_id, locale, updated_at)`
  (migration `00009_user_profile.sql`); guest forms send `locale`; mail renders in `en` or `nb`
  with locale-aware dates (§5 "Email localization keeps parity with today" — this is how).
- **Google Calendar sync is disabled for now** (decisions table, "Kept features"). The Go sync
  code (`internal/bookings/google.go`) stays, but Limen's OAuth link route cannot request the
  incremental calendar scopes, so the page editor's Google card was removed outright (commit
  `ea4a4c0`, not hidden behind a capability flag), and a page request asking for
  `googleSync: true` is refused up front with 400 `google_sync_unavailable`
  (`internal/bookings/handlers.go`'s `rejectGoogleSync`) before it ever reaches the service
  layer. `GET /api/v1/booking-pages/{id}/google-status` is hard-coded to
  `{"connected":false,"syncEnabled":false}` regardless of any linked account, and the README
  says the feature is not yet available. Re-enabling means a second Limen Google provider
  configured with the calendar scopes and `access_type=offline` — not a custom consent flow.
- **Playwright runs twice in CI** (§9): against `go run` (the existing fast job) and against the
  built Docker image started with compose's hardening flags (`read_only`, `cap_drop: [ALL]`,
  `no-new-privileges`) — the run that "keeps the hardening claims honest".
- **Security headers** (§7 said nothing; the old `src/start.ts` set them): every response carries
  a Content-Security-Policy whose `script-src` allows `'self'`, Turnstile's origin and one
  `sha256-` hash per inline `<script>` in the embedded `index.html` (computed once at boot — no
  `'unsafe-inline'` for scripts), `Permissions-Policy: camera=(), microphone=(), geolocation=()`,
  and `Strict-Transport-Security` when `APP_URL` is https. `/api/` request bodies are capped at
  1 MiB (413 `payload_too_large`); the server has a 30 s `ReadTimeout`.
- **Graceful shutdown** (§4/§7 said nothing): on SIGTERM the hub closes every WebSocket with a
  `GoingAway` close frame, waits for the handlers, deletes this replica's `ws_presence` rows, and
  the process waits for the hub and the job worker to unwind before closing the pool.
  `compose.yaml` gives it a 20 s `stop_grace_period`.
- **Landing stats have a REST read**: `GET /api/v1/stats` returns the `UsageStats` snapshot for
  first paint; the stats WebSocket remains the live source (§4 "Stats room").
- **Toolchain line: Go 1.26** (§7 `golang:1.25-alpine` superseded) — go.mod `go 1.26`,
  `golang:1.26-alpine`, CI `1.26`. The 1.25 line left Go's support window when 1.27 shipped.
- **Rate limiting** (decisions table, "Billing": abuse control = rate limiting only): poll
  creation/duplication and booking-page creation are limited at 20/min per IP alongside the
  existing vote/comment/claim/book/ws-connect buckets; every public limiter is a pass-through
  under `ENABLE_TEST_ROUTES` so the Playwright harness's single IP never trips one.

## Out of scope

- Data migration of any kind (no live users).
- Passkeys/WebAuthn (dropped with better-auth; revisit if Limen grows it or demand appears).
- SSR/prerendering/OG-tag injection.
- Helm chart, SQLite backend, built-in TLS.
- Web push notifications (the `spike/web-push-crypto` exploration stays parked).

## Risks

- **Limen maturity** (~520 stars, young): mitigated by the `internal/auth` seam, version
  pinning, and choosing only its core features.
- **Rewrite scale** (~25k LOC server layer): mitigated by the e2e oracle, TDD, and the domain
  logic being a port (known behavior), not new design.
- **SPA-only** link previews/SEO: accepted explicitly.
