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
| Auth library | [Limen](https://limenauth.dev/) (`github.com/thecodearcher/limen`), **its features only**: email+password, OAuth (Google), generic OIDC, magic links, TOTP 2FA, sessions. **Passkeys are dropped** (Limen has no WebAuthn). Orgs/invitations are our own domain code. |
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
- **Orgs are ours:** `organizations`, `members` (owner/admin/member), `invitations` tables,
  ported from `src/server/auth/`. Personal org auto-created on signup (post-signup hook).
  Invitation flow unchanged: email → accept link → `/accept-invitation/{id}`.
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
