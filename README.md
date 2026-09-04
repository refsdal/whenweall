<div align="center">

<img src="./web/public/logo.svg" alt="" width="72" height="72">

# whenweall

**Find a time everyone can make.**

Free, open-source scheduling polls — propose some dates, share one link, let everyone
vote, pick the winner. Ships as one static Go binary and one Postgres database.

[![CI](https://github.com/refsdal/whenweall/actions/workflows/ci.yml/badge.svg)](https://github.com/refsdal/whenweall/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Docker Image](https://img.shields.io/badge/ghcr.io-refsdal%2Fwhenweall-2496ED?logo=docker&logoColor=white)](https://github.com/refsdal/whenweall/pkgs/container/whenweall)

</div>

---

whenweall is a Doodle/Rallly-style scheduling poll that runs on your own hardware. An
organiser signs in, picks a handful of dates (or writes a plain list of options) and
shares a link. Anyone with the link votes **yes / if need be / no** — no account, no app,
no e-mail address required. Votes, comments and viewer presence stream to everyone
watching the page over a WebSocket, so the grid fills in live. When the organiser
finalises an option, every participant who left an e-mail gets a note with an `.ics`
attachment.

The same engine also runs **sign-up sheets**: slots with a capacity that people _claim_
instead of vote on — volunteer shifts, parent-teacher meetings, bring-a-dish lists — with
the same guest flow, live updates and e-mails as a scheduling poll.

whenweall also runs **1:1 booking pages** (Calendly-style "book 30 minutes with me"
links): an organiser sets weekly availability, and visitors pick an open slot in their
own time zone and book it, with a confirmation e-mail, an `.ics`, and a manage link to
cancel or reschedule.

**No Cloudflare, no Node, no Redis, no telemetry.** The whole backend is a single Go
binary that serves the API and the built SPA and needs nothing beside it but Postgres —
Postgres does the job a cache/broker/queue would otherwise be needed for too (`LISTEN`/
`NOTIFY` for WebSocket fan-out across replicas, presence and rate-limit tables, a
`scheduled_jobs` table for timers and the mail queue), so there is exactly one stateful
service to run and back up. whenweall phones home to nobody; the only outbound calls it
ever makes are the ones you configure yourself (your SMTP relay, and Google's OAuth
endpoints if you turn on Google sign-in).

> **Status.** v1, v2 (sign-up sheets) and v3 (booking pages) are feature-complete and
> pass their full test suite, but there is no public hosted instance — self-host your own
> with the [quickstart](#quickstart) below.

## Screenshots

There is no hosted instance, so no screenshots ship in this repo. Run
`bun run screenshots` ([`e2e/screenshots.spec.ts`](./e2e/screenshots.spec.ts)) against your own
checkout to generate them from the real app, including the landing page, a poll, the creator,
the dashboard, a sign-up sheet and a booking page — see
[`docs/screenshots/README.md`](./docs/screenshots/README.md) for what gets captured and where it
lands. CI never generates or commits them, so they only appear once someone deliberately runs
that script.

## Quickstart

You need Docker and a real SMTP relay (or [Mailpit](#trying-it-with-mailpit) for a trial —
see below). Five minutes, start to finish:

```bash
git clone https://github.com/refsdal/whenweall.git whenweall
cd whenweall
cp .env.example .env
echo "POSTGRES_PASSWORD=$(openssl rand -base64 24)" >> .env
echo "AUTH_SECRET=$(openssl rand -base64 32)" >> .env
```

That generates the two secrets `.env.example` leaves blank and appends them —
compose reads `.env` as plain `KEY=value` lines and does not run the `$(...)` inside
one for you, so the generating has to happen in your shell, not in the file (a later
line for the same key wins, so appending overrides the blank one `cp` just copied in).
Now open `.env` and set the handful of remaining values that have no safe default —
your SMTP relay's credentials:

```bash
# .env
SMTP_HOST=smtp.your-provider.example
SMTP_USER=...
SMTP_PASSWORD=...
```

(No relay handy? Skip ahead to [Mailpit](#trying-it-with-mailpit) for a trial with no
external account needed, then come back here.)

Then bring it up:

```bash
docker compose up -d
```

That pulls Postgres and the published app image (`ghcr.io/refsdal/whenweall:latest`; add
`--build` to build it from your checkout instead), runs migrations on boot, and starts
listening on `:3000`. Check it's alive:

```bash
curl http://localhost:3000/healthz
```

Open `http://localhost:3000`, sign up, create a poll. Put a [reverse proxy](#reverse-proxy)
in front before exposing this to the internet — the app itself speaks plain HTTP.

### Trying it with Mailpit

whenweall needs a real SMTP relay in production (see [Non-goals](#non-goals) — there is no
way around sending real e-mail, it's the product), but for a local trial,
[Mailpit](https://mailpit.axllent.org/) gives you a real SMTP server with no external
network or account needed. `compose.yaml` ships a commented-out `mailpit` service — uncomment
it, then point the app at it:

```bash
# .env
SMTP_HOST=mailpit
SMTP_PORT=1025
SMTP_SECURE=false
```

Every e-mail whenweall sends (verification links, confirmations, `.ics` invites, digests)
lands in Mailpit's own web UI at `http://localhost:8025` instead of a real inbox.

## Features

### For organisers

- **Two poll kinds, one engine** — pick days on a calendar (optionally with time slots, in
  your time zone), or write a free-text list of options like "Pizza / Sushi / Thai".
- **Three-step creator** with a live summary, per-day time slots and an "apply to all days"
  shortcut.
- **A voting deadline** that closes the poll automatically and e-mails you when it does.
- **Finalise an option** — the best one is pre-selected — which locks the poll, shows a
  banner with _Add to calendar_, and e-mails every participant who left an address.
- **Batched notifications**: new votes and comments are rolled up into at most one digest
  e-mail per poll every 10 minutes, and each poll's vote/comment notifications can be
  toggled independently.
- **Dashboard** with status, participant counts and deadlines, plus duplicate and delete.
- **Edit after publishing**, including a confirmation when removing an option that already
  has votes on it.
- **Sign in** with e-mail + password (the address must be verified before the account can be
  used), Google, or an external OIDC provider (which must assert verified e-mails).
- **Sign-up sheets** — a third poll kind whose options are slots: set a **capacity** per
  slot (or leave it unlimited) and a **max sign-ups per person** for the whole sheet, then
  export a **roster CSV** of who claimed what, with e-mails, from the admin bar.

### For participants

- **No account needed.** Type a name, tap the cells, done. An e-mail address is optional
  unless the organiser required one.
- **Change your mind later** — a guest's answer stays editable from the same browser via an
  HMAC-signed edit token (nothing stored at rest); signed-in voters can edit from any device.
- **Live grid.** Other people's votes, comments and presence appear without a refresh.
- **Your time zone, not the organiser's.** Date-time options render in the viewer's zone
  with a "Times shown in Europe/Oslo · change" control.
- **Comments** under the grid, with the organiser and the author able to delete.
- **Calendar export** — `.ics` download and an add-to-Google link once a poll is finalised.
- **English and Norwegian (bokmål)**, light and dark, keyboard-navigable, and gentle about
  `prefers-reduced-motion`.
- **Sign-up sheets, participant side** — one-click claims on a slot board, live spot
  counts as other people claim or leave, a "Full" state once capacity is reached, and a
  confirmation e-mail (with `.ics` for date/time slots) on your first claim.

### 1:1 booking pages

- **For organisers** — publish a page at `/book/<handle>/<slug>` with a **weekly
  availability grid** (per-weekday time ranges), slot duration, **buffers** before/after
  each meeting, a **minimum notice** and a **booking horizon**, and **date overrides**
  for one-off days off or extra hours. Turn on **reminder e-mails** 24 hours out, and see
  every booking — upcoming and past, with cancel — on the page's own roster. Google
  Calendar sync (busy-time blocking, events on your calendar) is **not available in v5
  yet** — see [Roadmap](#roadmap).
- **For visitors** — pick an open slot in **your own time zone**, no account needed.
  Booking gets you a confirmation e-mail with an `.ics` attachment and a **manage
  link** to cancel or reschedule later, no sign-in required — and if you lose that mail,
  the same `.ics` is always a click away from the manage page itself.

### Under the hood

- **One binary, one database.** `cmd/whenweall` serves the HTTP API, the WebSocket
  endpoints and the built SPA, runs scheduled jobs (digests, reminders) in-process, and
  runs its own migrations on boot. Postgres is the only other
  service — see [Architecture](#architecture).
- **Postgres is the only source of truth.** Every mutation is a single transaction;
  `FOR UPDATE` locking (not an external lock service) is what makes "two guests claim the
  last sign-up slot" or "two visitors book the same minute" resolve to exactly one
  winner.
- **Realtime fan-out over `LISTEN`/`NOTIFY`.** A `room_events` table is the durable log;
  each replica's hub subscribes to Postgres notifications and replays them to every
  WebSocket connection watching that poll or booking page — no external pub/sub, and a
  reconnecting client always gets a snapshot-then-replay it can dedupe against. See
  [`internal/rooms/PROTOCOL.md`](./internal/rooms/PROTOCOL.md) for the full wire contract.
- **A pure availability engine.** `internal/bookings` turns weekly rules, overrides,
  buffers, notice, horizon and a list of busy intervals into the exact slots a visitor
  can pick — deterministic and DST-safe, shared between the endpoint that renders the
  public page and the one that validates a booking.
- **Turnstile** (optional) on sign-in, sign-up, password reset, guest voting, commenting and
  booking, plus a per-IP rate
  limiter backed by Postgres on every public, unauthenticated endpoint.
- **Google sign-in and an external OIDC provider are both optional**, each all-or-nothing:
  half-configured credentials are treated as off, with a warning at boot rather than a
  broken button in the UI — see [Configuration](#configuration).
- **Every sign-up gets a personal organization** — polls, sign-up sheets and booking
  pages all belong to an organization, not to a user directly, so adding a teammate to
  existing content is a membership change, not a data migration: a member is invited by
  e-mail through the auth API (there is no invite-sending screen in the SPA yet — see
  [What's next](#whats-next)), and accepts from a link in that mail, which switches
  their active organization to the one they just joined and adds it to the account
  menu's organization switcher alongside their personal org. A booking URL's `<handle>`
  segment is the active organization's slug.
- **A staff-only support console** at `/admin` for the person self-hosting this: user
  lookup, lock/unlock, delete, and an append-only audit log — see
  [`docs/admin-console.md`](./docs/admin-console.md). There is no impersonation and no
  in-console way to grant staff to anyone else — see [Admin bootstrap](#admin-bootstrap).

## Architecture

```mermaid
flowchart LR
  B["Browser<br/>React 19 · TanStack Router (SPA)"]

  subgraph APP["cmd/whenweall — one Go binary"]
    direction TB
    H["net/http API + embedded SPA<br/>internal/httpserver"]
    RM["Realtime hub<br/>internal/rooms"]
    J["Job worker<br/>internal/jobs<br/>digests · reminders · housekeeping"]
    A["internal/auth<br/>Limen-backed sessions"]
  end

  PG[("Postgres<br/>rows · room_events · scheduled_jobs")]
  SMTP["Your SMTP relay"]

  B -- "fetch: /api/v1/*" --> H
  B -. "WebSocket: /api/v1/polls/:id/ws, /api/v1/booking-pages/:id/ws" .-> RM
  H -- "reads and writes: source of truth" --> PG
  RM -- "LISTEN/NOTIFY room_events" --> PG
  J -- "claims scheduled_jobs, FOR UPDATE SKIP LOCKED" --> PG
  J -- "verification · confirmations · digests · reminders" --> SMTP
  A -- "sessions, orgs, staff" --> PG
```

**A vote, end to end.** The browser calls `POST /api/v1/polls/:id/participants`. The
handler verifies the captcha token (if configured and the caller is anonymous), checks
the rate limiter, re-checks that the poll is still open, then — inside a single
transaction — inserts the participant and their votes, appends a `room_events` row, and
`pg_notify`s a pointer to it (`internal/rooms.Emit`, spec §4). All three writes commit or
roll back together, so an event exists if and only if the vote that produced it does:
there is no way for the notify to fail while the vote survives, or vice versa. Once that
transaction commits, every replica's hub — listening on the same Postgres channel — reads
the new `room_events` row and fans out a `poll.changed` frame to each connected client,
which refetches the poll and re-renders. Delivery to the browser is at-least-once, not
exactly-once (a redundant `NOTIFY`, or a resync after reconnect, can redeliver a frame the
client already applied), so the client dedupes by the event's `seq`, never assuming
each one arrives exactly once — see `internal/rooms/PROTOCOL.md`.

**A claim, end to end.** Sign-up sheets are a third poll type, `signup`, whose options
carry a `capacity` (`null` = unlimited). A claim is just a vote row with `answer = 'yes'`
— the same table, participants and edit tokens as a scheduling poll — but unlike a vote,
two people claiming the last spot in the same instant is a real race: the handler takes
a `SELECT ... FOR UPDATE` lock on the option row inside the transaction, so "count
current claims → compare to capacity → insert" can never interleave across two
concurrent requests.

**A booking, end to end.** `internal/bookings`' availability engine is a pure function:
weekly rules, date overrides, slot duration, buffers, notice, horizon and a list of busy
intervals in, a deterministic, DST-safe list of open slots out. The public page and the
booking endpoint call it with the same inputs — booked slots, with their buffers — so a
slot the visitor is shown is always still bookable a moment later, short of
someone else taking it first. That race is closed the same way a sign-up claim is: the
write that creates a booking runs inside a transaction that locks the page's relevant
rows, so two visitors racing for the same slot can never both win it. A confirmed booking
gets a deterministic, HMAC-derived manage token (the visitor's whole credential for the
manage link — nothing to store or leak from a database). A `booking.reminder` job,
scheduled per booking, e-mails both parties 24 hours out.

## Configuration

Every setting is a plain environment variable, read once at boot and validated before
the server starts — an instance that starts and then breaks on the first request that
touches a missing variable is strictly worse than one that refuses to boot at all.
`.env.example` has every one of these with a comment; `docker compose up` reads `.env`
automatically.

**Capability semantics: unset = feature invisible, never broken.** Every optional
integration below (Turnstile, Google sign-in, OIDC) is all-or-nothing: leave
every variable for it unset and the feature simply doesn't appear in the UI. Set *some
but not all* of a group and the feature stays off too, but the server logs a warning at
boot — a half-configured integration failing silently at request time, pointing nowhere
near the actual typo, is worse than refusing to light up at all.

| Name                   | Required | Default                          | Purpose                                                                                       |
| ---------------------- | -------- | --------------------------------- | ----------------------------------------------------------------------------------------------- |
| `APP_URL`               | yes      | —                                  | Canonical origin, absolute `http(s)://…`, no trailing slash. Used for links in e-mails, OAuth callbacks and WebSocket origin checks. **Rotating this breaks every outstanding link that embeds it.** |
| `APP_ENV`               | no       | `development`                     | `development`, `test` or `production`. Gates whether `ENABLE_TEST_ROUTES` is even allowed. Also relaxes Limen's auth origin check to trust `http://localhost:5173` (Vite) — but only when `APP_ENV=development` **and** `APP_URL` is a loopback address (`localhost`/`127.0.0.1`/`::1`); a real deployment that forgets to set `APP_ENV=production` keeps the strict check because its `APP_URL` isn't loopback. |
| `PORT`                  | no       | `3000`                             | The port the server listens on inside its container.                                             |
| `DATABASE_URL`          | yes      | —                                  | `postgres://user:pass@host:5432/db`.                                                              |
| `DATABASE_POOL_SIZE`    | no       | `10`                                | Max open connections (1–100).                                                                     |
| `AUTH_SECRET`           | yes      | —                                  | ≥ 32 random bytes, e.g. `openssl rand -base64 32`. Signs sessions and derives every booking's manage-link token. **Rotating this invalidates every active session AND every outstanding booking manage link** — see the note below. |
| `SMTP_HOST`             | yes      | —                                  | whenweall cannot function without e-mail — see [Non-goals](#non-goals).                          |
| `SMTP_PORT`             | no       | `587`                               | —                                                                                                  |
| `SMTP_USER`             | no       | —                                  | Half-configured with `SMTP_PASSWORD` (one set, one not) sends unauthenticated, with a boot warning. |
| `SMTP_PASSWORD`         | no       | —                                  | See above.                                                                                         |
| `SMTP_SECURE`           | no       | `false`                             | Implicit TLS. Leave `false` for STARTTLS on port 587 (the common case); `true` normally pairs with port 465. |
| `EMAIL_FROM`            | no       | `whenweall <no-reply@localhost>`  | `From:` on every outgoing e-mail.                                                                  |
| `TURNSTILE_SITE_KEY`    | no       | —                                  | Optional captcha on guest voting, commenting, sign-up claims and booking, and on sign-in/sign-up/password reset. Needs `TURNSTILE_SECRET_KEY` too. |
| `TURNSTILE_SECRET_KEY`  | no       | —                                  | See above. Without this pair, no endpoint asks for a captcha (the UI hides the widget) — fine for a private instance, worth knowing for one on the open internet. |
| `GOOGLE_CLIENT_ID`      | no       | —                                  | Optional "Continue with Google". Needs `GOOGLE_CLIENT_SECRET` too. Register `<APP_URL>/api/v1/auth/oauth/google/callback` as the **Authorized redirect URI** in Google Cloud Console — a mismatch there is the single most common OAuth misconfiguration. |
| `GOOGLE_CLIENT_SECRET`  | no       | —                                  | See above.                                                                                          |
| `OIDC_ISSUER`           | no       | —                                  | Optional external SSO. Needs `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` too (all three, not a pair). Register `<APP_URL>/api/v1/auth/oauth/<OIDC_NAME>/callback` (default `.../oauth/sso/callback`) as the redirect URI at your provider. The issuer must assert `email_verified: true` in its userinfo/ID token: a sign-in whose email is not verified by the IdP is refused, because an OIDC email is what links the sign-in to an existing account. |
| `OIDC_CLIENT_ID`        | no       | —                                  | See above.                                                                                          |
| `OIDC_CLIENT_SECRET`    | no       | —                                  | See above.                                                                                          |
| `OIDC_NAME`             | no       | `sso`                                | Label on the OIDC sign-in button.                                                                  |
| `TRUST_PROXY`           | no       | `false`                              | Trust `X-Forwarded-*` from the reverse proxy in front of the app (client IPs for rate limiting). Set `true` only when — and because — a reverse proxy in front of the app sets these headers; otherwise a client can fabricate them and bypass every per-IP rate limit. |
| `MIGRATE_ON_BOOT`       | no       | `true`                               | Escape hatch: set `false` to run `whenweall migrate` yourself instead — see [Ops](#ops).           |
| `ENABLE_TEST_ROUTES`    | no       | `false`                              | Exposes the Playwright seed route. The server refuses to start if this is set alongside `APP_ENV=production` — never set it on a real instance. |

**`AUTH_SECRET` rotation invalidates sessions and booking manage links.** Nothing about
`AUTH_SECRET` is stored per-session or per-booking: every session token and every
booking's manage-link token is deterministically derived from it. Rotate it and every
signed-in user is signed out, and every outstanding "manage your booking" e-mail link
anyone is still holding onto stops working. Rotate deliberately, not as a routine.

**Google Calendar sync is not available in v5 yet.** The `GOOGLE_CLIENT_ID`/
`GOOGLE_CLIENT_SECRET` pair only powers "Continue with Google", which requests the
default openid/email/profile scopes and nothing else. The v3 booking-page calendar
integration (busy-time blocking, events on the organiser's calendar) has not been
re-enabled in the Go backend: the page editor shows no Google card, the API refuses
`googleSync: true`, and `GET /api/v1/booking-pages/{id}/google-status` always answers
"not connected". You do not need to add any calendar scopes to your OAuth consent screen.

## Reverse proxy

whenweall speaks plain HTTP and does not terminate TLS itself (see
[Non-goals](#non-goals)) — put a reverse proxy in front for anything but a local trial.
[Caddy](https://caddyserver.com/) gets you automatic HTTPS in two lines:

```caddyfile
whenweall.example.com {
	reverse_proxy localhost:3000
}
```

Whatever proxy you use, it must forward WebSocket upgrades — every poll, sign-up sheet
and booking page's live updates depend on it. Once a proxy is in front of the app and
setting `X-Forwarded-For`, set `TRUST_PROXY=true` so client IPs used for rate limiting
come from that header instead of the proxy's own address — leaving it at its `false`
default with a proxy in front means every request appears to originate from the same
IP. **nginx** needs the upgrade headers set explicitly:

```nginx
location / {
    proxy_pass http://localhost:3000;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

**Traefik** forwards WebSocket upgrades by default with no extra configuration beyond
routing to the container's port — just make sure you set `TRUST_PROXY=true` so client
IPs used for rate limiting come from the forwarded headers Traefik sets, not the
proxy's own address.

## Ops

**Backup** is `pg_dump` against the one database — there is no other state anywhere.

```bash
docker compose exec db pg_dump -U whenweall whenweall > backup.sql
```

**Upgrade** is pulling a new image tag and restarting — migrations run themselves on
boot (`MIGRATE_ON_BOOT=true`, the default):

```bash
docker compose pull app
docker compose up -d app
```

Running from a source checkout instead of the published image? The Dockerfile compiles nothing —
build the binary first, then rebuild and restart in one step; migrations still run themselves on
boot:

```bash
bash scripts/build-artifacts.sh linux/amd64   # needs Go and bun on the host
docker compose up -d --build app
```

Prefer to control exactly when migrations run (e.g. running several replicas and wanting
exactly one to migrate)? Set `MIGRATE_ON_BOOT=false` and run them yourself first:

```bash
docker compose run --rm app /whenweall migrate
docker compose up -d app
```

## Admin bootstrap

The first staff account (access to the `/admin` support console) has no self-serve path
— granting it is a deliberate, one-time operator action:

```bash
docker compose exec app /whenweall create-staff-user --email you@example.com
```

Idempotent: re-running it against an already-staffed e-mail succeeds silently. This
command is the **only** way to grant staff — there is no in-console promotion, and (per
[`docs/admin-console.md`](./docs/admin-console.md)) granting it is deliberately never
audited, unlike everything staff do once they have it. Full runbook, including revoking,
reading the audit log and incident response, in
[`docs/admin-console.md`](./docs/admin-console.md).

## Development

You need [Docker](https://www.docker.com/), [Go](https://go.dev/) 1.26+, and
[bun](https://bun.sh/) (see [`web/.bun-version`](./web/.bun-version)) — no `npm`/`pnpm`.

```bash
git clone https://github.com/refsdal/whenweall.git whenweall
cd whenweall

cp .env.example .env             # fill in AUTH_SECRET, SMTP_HOST, POSTGRES_PASSWORD, etc. — see
                                  # below. Compose interpolates the WHOLE file at config load, so
                                  # its required-variable guards abort even a `db`-only `up` if
                                  # you run it before .env exists and is filled in.
docker compose up -d db          # Postgres only — the app itself runs outside the container

# The binary reads only the process environment — nothing loads .env for you outside compose.
set -a; . ./.env; set +a
export DATABASE_URL="postgres://whenweall:${POSTGRES_PASSWORD}@localhost:${POSTGRES_PORT:-5433}/whenweall?sslmode=disable"
export APP_ENV=development
go run ./cmd/whenweall           # migrates on boot, serves the API on :3000

cd web
bun install                      # also compiles the Paraglide messages
bun dev                          # Vite on :5173, proxying /api to :3000
```

`go run ./cmd/whenweall migrate` and `... create-staff-user` read the same variables, so run them
from the same shell (or prefix them the same way). With `APP_ENV=development` the server also
trusts `http://localhost:5173` as an OAuth `redirect_uri` origin, so "Continue with Google" works
from the Vite dev server too.

Open `http://localhost:5173`. The API server itself also serves the last **built** SPA
at `http://localhost:3000` (whatever `cd web && bun run build` last produced) — useful
for testing the exact thing Docker ships, but `bun dev`'s hot reload is what you want
while actually changing frontend code.

Running the Go test suite needs Docker too — it spins up its own throwaway Postgres via
[testcontainers-go](https://golang.testcontainers.org/), migrates a template database
once, and clones it per package, so `go test ./...` works identically on a laptop and in
CI, with no mocked database anywhere:

```bash
go test ./...
go vet ./...
golangci-lint run ./...
```

End-to-end tests (Playwright, the oracle for this whole rewrite) run against the real
built SPA served by the real Go binary on `:3100` — see
[`e2e/run-server.sh`](./e2e/run-server.sh), the harness `playwright.config.ts`'s
`webServer` drives. It starts a throwaway Postgres and a pinned [Mailpit](https://mailpit.axllent.org/)
(the specs read the inbox through Mailpit's HTTP API on `:8026` to follow verification, reset,
invitation and booking links) and removes exactly the containers it started when the run ends.
Turnstile is off in this harness; the server-side verifier has its own Go tests.

```bash
bunx playwright install --with-deps chromium   # once
bunx playwright test
```

To iterate quickly, keep the server running yourself and let Playwright reuse it
(`reuseExistingServer`) — a run then neither restarts the server nor drops its database:

```bash
bash e2e/run-server.sh    # in one terminal (needs the env playwright.config.ts would pass; see the script header)
bunx playwright test e2e/comments.spec.ts   # in another, as often as you like
```

The same suite also runs against the **built Docker image** with the compose hardening flags
(read-only root, all capabilities dropped, no-new-privileges, user 65532) — what CI's
`e2e-image` job does:

```bash
bash scripts/build-artifacts.sh linux/amd64   # the image build itself compiles nothing
docker build -t whenweall:e2e .
e2e/compose-e2e.sh up -d --wait      # compose.yaml + compose.e2e.yaml, app on :3100
e2e/assert-hardening.sh              # proves the flags are live on the running container
E2E_SERVER=image bunx playwright test
e2e/compose-e2e.sh down -v
```

## Testing

Three layers, all runnable from a clean checkout:

- **Go** (`go test ./...`) — every domain package (`internal/polls`, `internal/bookings`,
  `internal/auth`, `internal/rooms`, `internal/admin`, …) against a real Postgres via
  testcontainers, including a dedicated concurrency pass under the race detector for the
  handful of "two requests racing the same slot" tests that prove exactly one wins.
- **Frontend unit** (`cd web && bunx vitest run`) — component behaviour via Testing
  Library, the typed API client, time-zone and availability-rendering helpers, and
  message-catalogue parity between `en`/`nb`.
- **End-to-end** (`bunx playwright test`, Chromium) — every journey a user can take, against the
  real binary and the real built SPA, with e-mail read back out of Mailpit: sign-up → verification
  link → sign-in (and the unverified card's resend), forgot → reset link → new password; the full
  create → vote → edit → finalise → `.ics` flow with the "N people were notified" toast and the
  decided mail; a choice poll and a time-slot dates poll built through the wizard (with the
  time-zone switch); editing a voted poll (lost-vote confirmation) and a deadline closing it for a
  guest; comments posted and deleted live across two tabs; a signed-in voter editing their answer
  from a second device; a live update landing in a second browser context; dashboard
  duplicate/delete; the locale switch (cookie and, when signed in, the profile); settings (rename,
  locale, password-checked account deletion); the sign-up sheet journey (a guest claims a slot, a
  second guest sees it full and claims another, the change appears live in the first guest's tab,
  and the owner downloads the roster CSV); the booking journey on a seeded page (book, `.ics`,
  live slot removal, organiser view, cancel) and on a page created through the UI (handle on
  `/settings`, `/bookings/new`, book, reschedule from the manage link, confirmation and
  rescheduled mails); an organisation invitation accepted from the mail with the org switch; and
  the admin console (stats, search, lock/unlock/delete with reasons and the audit log, the
  dead-letter jobs page with retry, plus the not-found page and 403/401 an outsider gets).
  CI runs this suite twice: against `go run` (`e2e`) and against the built Docker image under
  compose's hardening flags (`e2e-image`, see [Development](#development)).

Running just one thing:

```bash
go test ./internal/bookings/...
go test -race -run 'Race|Concurrent' ./internal/polls/... ./internal/bookings/...
cd web && bunx vitest run src/components/poll/__tests__/VoteGrid.test.tsx
bunx playwright test e2e/poll-flow.spec.ts
bunx playwright test e2e/auth-email.spec.ts     # the Mailpit-backed flows
E2E_SERVER=image bunx playwright test e2e/smoke.spec.ts   # against the running image stack
bunx playwright test --ui   # interactive
```

## Project structure

```
cmd/whenweall/           the one binary: serve, migrate, healthcheck, create-staff-user, version
internal/
  config/                env parsing and validation — the single source of app configuration
  db/                     connection pool, goose migration runner, id generation
  auth/                   Limen-backed sessions, organizations, staff, the create-staff-user path
  admin/                  the /admin staff console: user lookup, lock/unlock, delete, audit log
  polls/                  scheduling polls + sign-up sheets: service layer, HTTP handlers, sqlc queries
  bookings/               1:1 booking pages: availability engine, service layer, HTTP handlers, .ics
  rooms/                  the realtime hub: LISTEN/NOTIFY fan-out, WebSocket routes, PROTOCOL.md
  jobs/                   scheduled-job worker: digests, reminders, housekeeping
  mailer/                 SMTP transport + html/template rendering
  ics/                    shared RFC 5545 building blocks (escaping, folding, VCALENDAR wrapper)
  httpserver/             mux wiring, auth middleware, rate limiting, the embedded SPA (go:embed)
migrations/               goose SQL migrations (the schema's own source of truth)
web/                      the SPA — React 19, TanStack Router, Vite, Tailwind
  src/api/                typed fetch client against the Go backend's REST + WS surface
  src/components/         poll/, signup/, booking/, admin/, dashboard/, landing/, auth/, ui/
  src/lib/                time zone, i18n, room-socket client (replay/reconnect over the WS protocol)
  messages/               en.json, nb.json — the translation source of truth
e2e/                      Playwright specs and fixtures — the oracle this whole rewrite was proven against
docs/                     admin runbook, Limen schema-regen steps, screenshot script docs
compose.yaml              self-host deployment: one app container, one Postgres
scripts/build-artifacts.sh  builds the SPA once and cross-compiles one static binary per architecture
Dockerfile                COPYs the prebuilt binary into distroless — nothing compiles inside it
```

## Internationalisation

Strings live in [`web/messages/en.json`](./web/messages/en.json) and
[`web/messages/nb.json`](./web/messages/nb.json) and are compiled by
[Paraglide](https://inlang.com/m/gerre34r/library-inlang-paraglideJs) into tree-shakeable
functions (`m.poll_share_title()`). The active locale resolves from the
`whenweall_locale` cookie, then `Accept-Language`, then English. A unit test fails the
build if the two catalogues drift apart in keys or placeholders. A signed-in user's choice
is also stored server-side (`user_preferences.locale`) and used for every e-mail sent to
them; guest forms send the visitor's locale along with the vote/claim/booking.

To add a locale — say German:

1. Add `"de"` to `locales` in [`web/project.inlang/settings.json`](./web/project.inlang/settings.json).
2. Copy `web/messages/en.json` to `web/messages/de.json` and translate it, including the
   `locale_de` label used by the switcher.
3. Map it to a BCP-47 tag for `Intl` in `intlLocale()` in
   [`web/src/lib/i18n.ts`](./web/src/lib/i18n.ts).
4. `cd web && bun install` (the `postinstall` hook recompiles Paraglide) and
   `bunx vitest run`.

## Roadmap

- **v1 — done.** Group date/time and free-text polls, organiser accounts (password,
  Google, external OIDC), guest voting with later edits, comments, deadlines, finalising
  with `.ics`, live updates, e-mail notifications, English + Norwegian.
- **v2 — done.** Sign-up sheets: a `signup` poll type whose options are capacity-limited
  slots, claimed (not voted on) by participants, with a per-person claim limit,
  transaction-serialised capacity enforcement, a claim confirmation e-mail and an
  owner-only roster CSV export.
- **v3 — done.** 1:1 booking pages: weekly availability with buffers, notice and a
  booking horizon, date overrides, optional Google Calendar conflict-checking and event
  sync, transaction-serialised booking to prevent double-booking, visitor
  cancel/reschedule via a manage link, reminder e-mails, and an organiser roster per
  page.
- **Go rewrite — done.** The whole backend left Cloudflare Workers/D1/Durable Objects
  for a single Go binary and Postgres — see [Architecture](#architecture). One v3
  feature did not make the cut yet: Google Calendar sync (below).

### What's next

No date attached yet — ideas under consideration:

- **Round-robin / team pages** — one link that distributes bookings across several
  organisers' calendars.
- **Google Calendar sync (return)** — re-enable v3's busy-time blocking and event
  creation once incremental consent for the calendar scopes is wired through the auth
  layer; the Go sync code is in the tree, dormant.
- **Google Meet links** — once calendar sync is back, auto-attach a Meet link to the
  event a booking creates, instead of a plain text location.
- **Waitlists** — let a visitor join a full or past-notice slot and get offered it if
  it opens back up.
- **Invite-sending UI** — a screen in the product to send an organization invitation.
  Today an invitation can only be created by calling the auth API directly; accepting
  one (the mailed link, the accept page, the organization switcher) is fully built —
  see [Under the hood](#under-the-hood).

### Non-goals

Deliberately out of scope for now:

- **No Helm chart.** `compose.yaml` is the one supported deployment shape; a Helm chart
  is a real maintenance commitment this project isn't taking on yet.
- **No SQLite mode.** Postgres's `LISTEN`/`NOTIFY` and row locking are load-bearing for
  realtime fan-out and the double-booking/double-claim guarantees — there is no SQLite
  equivalent to port to without giving those up.
- **No built-in TLS.** whenweall speaks plain HTTP; bring a [reverse proxy](#reverse-proxy).
- Hidden votes, invitee reminders on polls, a moderation console, magic-link sign-in,
  payments, and (for booking pages) Outlook/CalDAV sync and recurring bookings.

## Contributing

Pull requests are welcome — see [CONTRIBUTING.md](./CONTRIBUTING.md). In short: the
project is developed test-first, commits follow
[Conventional Commits](https://www.conventionalcommits.org/), and
`go test ./... && go vet ./... && golangci-lint run ./...` plus
`cd web && bun run typecheck && bun run lint && bunx vitest run` should be green before
you open a PR.

## Security

Please **do not** open a public issue for a vulnerability. Report it privately through
[GitHub's advisory form](https://github.com/refsdal/whenweall/security/advisories/new)
or the address in [SECURITY.md](./.github/SECURITY.md), which also carries the
repository-settings checklist for the owner (branch protection, secret scanning, Dependabot
alerts, private vulnerability reporting) — those live in the GitHub UI, not in this repo.

## Notes

- **bun only** for the frontend toolchain — there is no `package-lock.json`, and CI uses
  bun too. `npm`/`pnpm` are not supported.
- **`web/src/paraglide/` is generated** and git-ignored; `bun install` recreates it.
- **`web/src/routeTree.gen.ts` is generated** by `bun run generate-routes` (TanStack
  Router's file-based router) and is committed so a fresh clone typechecks immediately.

## Acknowledgements

Inspired by [Doodle](https://doodle.com/) and by [Rallly](https://rallly.co/), whose
open-source take on scheduling polls set the bar. Built on
[TanStack Router](https://tanstack.com/router), [Limen](https://github.com/thecodearcher/limen),
[goose](https://github.com/pressly/goose), [sqlc](https://sqlc.dev/),
[Tailwind CSS](https://tailwindcss.com/) and [shadcn/ui](https://ui.shadcn.com/).

## Licence

[MIT](./LICENSE) © 2026 Anders Refsdal Olsen
