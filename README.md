<div align="center">

<img src="./public/logo.svg" alt="" width="72" height="72">

# samla

**Find a time everyone can make.**

Free, open-source scheduling polls — propose some dates, share one link, let everyone
vote, pick the winner. Built to run natively on Cloudflare Workers.

[![CI](https://github.com/andersro93/scheduler/actions/workflows/ci.yml/badge.svg)](https://github.com/andersro93/scheduler/actions/workflows/ci.yml)
[![CodeQL](https://github.com/andersro93/scheduler/actions/workflows/codeql.yml/badge.svg)](https://github.com/andersro93/scheduler/actions/workflows/codeql.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)
[![Runs on Cloudflare Workers](https://img.shields.io/badge/runs%20on-Cloudflare%20Workers-F38020?logo=cloudflare&logoColor=white)](https://workers.cloudflare.com/)
[![Built with Bun](https://img.shields.io/badge/built%20with-bun-14151a?logo=bun&logoColor=fbf0df)](https://bun.sh/)

</div>

---

samla is a Doodle/Rallly-style scheduling poll that fits in a single Cloudflare Worker.
An organiser signs in, picks a handful of dates (or writes a plain list of options) and
shares a link. Anyone with the link votes **yes / if need be / no** — no account, no app,
no e-mail address required. Votes, comments and viewer presence stream to everyone
watching the page over a WebSocket, so the grid fills in live. When the organiser
finalises an option, every participant who left an e-mail gets a note with an `.ics`
attachment.

The whole thing is one Worker, one D1 database and one Durable Object class. There is no
Node server, no container and no queue: it deploys with `bun run deploy` and costs the
$5/month Workers Paid plan.

> **Status.** v1 is feature-complete and passes its full test suite, but there is no
> public hosted instance yet — deploy your own with the [Cloudflare setup](#cloudflare-setup)
> below.

## Screenshots

> The images below are captured from the real app by `bun run screenshots`
> ([`e2e/screenshots.spec.ts`](./e2e/screenshots.spec.ts)) and committed by hand — run it on a
> machine with Chromium installed. CI never generates or commits them, so if they don't render
> here, they simply haven't been captured for this checkout yet.

|                                                                                            |                                                                       |
| ------------------------------------------------------------------------------------------ | --------------------------------------------------------------------- |
| ![The landing page in light mode](./docs/screenshots/landing-light.png)                    | ![The landing page in dark mode](./docs/screenshots/landing-dark.png) |
| ![A poll being voted on, with the winning column highlighted](./docs/screenshots/poll.png) | ![The three-step poll creator](./docs/screenshots/creator.png)        |

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
- **Sign in** with e-mail + password (verified), Google, or a passkey.

### For participants

- **No account needed.** Type a name, tap the cells, done. An e-mail address is optional
  unless the organiser required one.
- **Change your mind later** — a guest's answer stays editable from the same browser via a
  hashed edit token; signed-in voters can edit from any device.
- **Live grid.** Other people's votes, comments and presence appear without a refresh.
- **Your time zone, not the organiser's.** Date-time options render in the viewer's zone
  with a "Times shown in Europe/Oslo · change" control.
- **Comments** under the grid, with the organiser and the author able to delete.
- **Calendar export** — `.ics` download and an add-to-Google link once a poll is finalised.
- **English and Norwegian (bokmål)**, light and dark, keyboard-navigable, and gentle about
  `prefers-reduced-motion`.

### Under the hood

- **One Worker.** SSR, server functions, auth, the WebSocket endpoint and the `.ics` route
  are all the same deployment.
- **D1 is the only source of truth.** Mutations write to D1 in a batch and _then_ poke the
  Durable Object best-effort; a DO or e-mail failure never fails a user request.
- **Hibernating WebSockets.** `PollRoom` uses the hibernation API, so idle poll pages cost
  nothing while staying connected.
- **Turnstile** on sign-up, sign-in and password reset (Better-Auth's captcha plugin) and on
  guest voting and commenting; a Workers rate-limiter binding additionally caps poll creation,
  voting and commenting per IP.
- **Strict CSP**, HSTS in production, `nosniff`, `Referrer-Policy` and a `Permissions-Policy`
  applied by a request middleware to every response.
- **279 tests** across three runners: jsdom unit tests, integration tests in real `workerd`
  with a real D1 and Durable Object, and Playwright end-to-end flows.

## Architecture

```mermaid
flowchart LR
  B["Browser<br/>React 19 · TanStack Router"]

  subgraph CF["Cloudflare"]
    direction TB
    W["Worker — TanStack Start<br/>SSR · server functions · /api/*"]
    R["PollRoom<br/>Durable Object, hibernating"]
    D[("D1 · samla-db<br/>Drizzle ORM")]
    E["Email Service<br/>send_email binding"]
    T["Turnstile"]
  end

  B -- "SSR document, server-function RPC" --> W
  B -. "WebSocket /api/polls/:id/ws" .-> R
  R -. "poll.changed · presence" .-> B
  W -- "reads and writes: source of truth" --> D
  W -- "broadcast · enqueueDigest · syncDeadline" --> R
  W -- "verify captcha token" --> T
  W -- "verification · reset · finalised" --> E
  R -- "alarms: digest, deadline auto-close" --> D
  R -- "digest · deadline-closed" --> E
```

**A vote, end to end.** The browser calls the `addParticipant` server function. The Worker
verifies the Turnstile token, checks the rate limiter, re-checks that the poll is still
open, then inserts the participant and their votes into D1 in a single `batch()`. Only
after D1 has committed does it call `env.POLL_ROOM.getByName(pollId)` to `broadcast` a
`poll.changed` event and `enqueueDigest` an item for the organiser. Every connected client
receives `poll.changed` and invalidates its route, which refetches the poll from D1 — so what
everyone sees is always what the database says, and no delta bookkeeping can drift.
If the Durable Object call throws, the vote is still saved and the request still succeeds.

The DO owns only ephemeral state: connected sockets, the pending digest buffer and the
next alarm time. Alarms fire the organiser digest (at most one per poll per 10 minutes,
retried up to 3 times) and the deadline auto-close.

## Quick start

You need [bun](https://bun.sh/) 1.4+ (see [`.bun-version`](./.bun-version)). No Node
installation is required.

```bash
git clone https://github.com/andersro93/scheduler.git samla
cd samla

bun install                     # also compiles the Paraglide messages
cp .dev.vars.example .dev.vars  # local secrets — the defaults are Turnstile test keys
bun run cf-typegen              # generates worker-configuration.d.ts from wrangler.jsonc
bun run db:migrate:local        # applies drizzle/ migrations to the local D1
bun run dev                     # http://localhost:3000
```

That is a fully working app: local D1, a local Durable Object, Turnstile's always-pass
test keys and a console "mailer" that prints verification and reset links to the terminal
instead of sending them. Sign up, click the link printed in your terminal, create a poll.

Google sign-in stays hidden until `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` are set.

## Configuration

Public values live in `wrangler.jsonc` under `vars`. Secrets live in `.dev.vars` locally
and in Cloudflare's secret store in production (`bunx wrangler secret put NAME`). Never
commit `.dev.vars` — it is git-ignored.

| Name                   | Kind                | Where it is set                                     | Purpose                                                                                          |
| ---------------------- | ------------------- | --------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `APP_URL`              | var                 | `wrangler.jsonc`                                    | Canonical origin. Used for links in e-mails, the passkey origin and share URLs.                  |
| `APP_ENV`              | var                 | `wrangler.jsonc`                                    | `development` or `production`. Gates HSTS and hard-disables the test seed route.                 |
| `EMAIL_FROM`           | var                 | `wrangler.jsonc`                                    | Sender of every outgoing e-mail, e.g. `samla <no-reply@example.com>`.                            |
| `TURNSTILE_SITE_KEY`   | var                 | `wrangler.jsonc`                                    | Public Turnstile key rendered into the widget.                                                   |
| `BETTER_AUTH_SECRET`   | secret              | `.dev.vars` / `wrangler secret put`                 | Signs sessions and tokens. 32+ random bytes. **Required.**                                       |
| `TURNSTILE_SECRET_KEY` | secret              | `.dev.vars` / `wrangler secret put`                 | Server-side captcha verification. **Required.**                                                  |
| `GOOGLE_CLIENT_ID`     | secret              | `.dev.vars` / `wrangler secret put`                 | Optional. Enables the "Continue with Google" button.                                             |
| `GOOGLE_CLIENT_SECRET` | secret              | `.dev.vars` / `wrangler secret put`                 | Optional, required alongside the client id.                                                      |
| `ENABLE_TEST_ROUTES`   | secret (local only) | `.dev.vars`                                         | `true` exposes `POST /api/test/seed` for Playwright. Ignored when `APP_ENV=production`.          |
| `DB`                   | binding             | `wrangler.jsonc` → `d1_databases`                   | The D1 database (`samla-db`). Holds every durable row.                                           |
| `POLL_ROOM`            | binding             | `wrangler.jsonc` → `durable_objects` + `migrations` | The `PollRoom` class: sockets, digest buffer, alarms.                                            |
| `EMAIL`                | binding             | `wrangler.jsonc` → `send_email`                     | Cloudflare Email Service. Without it, mail is logged to the console instead of sent.             |
| `RATE_LIMITER`         | binding             | `wrangler.jsonc` → `ratelimits`                     | 20 requests / 60 s per action, keyed by `action:ip` across poll creation, voting and commenting. |

CI and the deploy workflow additionally need two GitHub secrets — see
[Deploying](#deploying).

## Cloudflare setup

Everything below is a one-time setup for your own instance.

**0. Prerequisites.** A Cloudflare account on the **Workers Paid** plan ($5/month) —
Durable Objects and Email Service are not available on the free plan — and a domain using
Cloudflare DNS if you want real e-mail. Then:

```bash
bunx wrangler login
```

**1. Create the D1 database.**

```bash
bunx wrangler d1 create samla-db
```

Copy the printed `database_id` into `wrangler.jsonc` (it ships with an all-zero placeholder
so local development works before you have an account), then apply the migrations:

```bash
bun run db:migrate:remote
```

**2. Set the secrets.**

```bash
bunx wrangler secret put BETTER_AUTH_SECRET     # e.g. `openssl rand -base64 32`
bunx wrangler secret put TURNSTILE_SECRET_KEY
bunx wrangler secret put GOOGLE_CLIENT_ID       # optional
bunx wrangler secret put GOOGLE_CLIENT_SECRET   # optional
```

**3. Point `APP_URL` at your domain** in `wrangler.jsonc`, set `APP_ENV` to `production`
and `EMAIL_FROM` to an address on a domain you control. `APP_URL` must match the deployed
origin exactly — passkeys and OAuth callbacks are origin-bound.

**4. Turnstile.** In the Cloudflare dashboard → **Turnstile** → _Add widget_, add your
domain and copy the two keys: the **site key** goes into `TURNSTILE_SITE_KEY` in
`wrangler.jsonc`, the **secret key** into the secret above. The values in
`.dev.vars.example` and `wrangler.jsonc` are Cloudflare's documented always-pass
[test keys](https://developers.cloudflare.com/turnstile/troubleshooting/testing/) — fine
for local development and CI, useless in production.

**5. Email Service.** samla sends through the
[`send_email` binding](https://developers.cloudflare.com/email-routing/email-workers/send-email-workers/)
(`{ "name": "EMAIL" }`, already in `wrangler.jsonc`). Add the sending domain to Cloudflare
and verify it — the dashboard walks you through the DKIM/SPF records. Until a verified
sender exists, `EMAIL_FROM` will be rejected at send time; with no `EMAIL` binding at all,
samla falls back to logging each message to the Worker console, which is exactly what
local development uses.

**6. Google OAuth** (optional). Create an OAuth client in the Google Cloud console and add
the authorised redirect URI:

```
https://<your-domain>/api/auth/callback/google
```

**7. Durable Object migration.** Nothing to do — the `v1` migration that creates the
SQLite-backed `PollRoom` class is already declared in `wrangler.jsonc` and is applied on
first deploy.

### Deploying

```bash
bun run deploy    # = bun run build && wrangler deploy
```

Or let GitHub do it: [`.github/workflows/deploy.yml`](./.github/workflows/deploy.yml) runs
the remote migrations and deploys on every push to `main`. It needs a repository
environment named **`production`** holding two secrets:

| Secret                  | Where to get it                                                                  |
| ----------------------- | -------------------------------------------------------------------------------- |
| `CLOUDFLARE_API_TOKEN`  | Cloudflare dashboard → _My Profile_ → _API Tokens_ → **Edit Cloudflare Workers** |
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare dashboard → _Workers & Pages_ → account id in the sidebar             |

You can always rehearse a deploy without touching your account:

```bash
bunx wrangler deploy --dry-run --outdir /tmp/samla-dryrun
```

## Scripts

| Script                      | What it does                                                                   |
| --------------------------- | ------------------------------------------------------------------------------ |
| `bun run dev`               | Vite dev server on port 3000, with the Worker running in workerd.              |
| `bun run build`             | Production build into `dist/` (client + server).                               |
| `bun run preview`           | Build, then serve the **built** Worker via `vite preview`.                     |
| `bun run deploy`            | Build, then `wrangler deploy`.                                                 |
| `bun run typecheck`         | `tsc --noEmit`.                                                                |
| `bun run lint`              | ESLint over the whole repo.                                                    |
| `bun run format`            | Prettier, writing in place (`format:check` to only verify).                    |
| `bun run test`              | Every Vitest project — unit _and_ workers.                                     |
| `bun run test:unit`         | jsdom unit + component tests only.                                             |
| `bun run test:workers`      | Integration tests in real workerd, against real D1 and Durable Objects.        |
| `bun run test:e2e`          | Playwright, against a freshly built Worker.                                    |
| `bun run screenshots`       | Regenerates `docs/screenshots/*.png` from the running app.                     |
| `bun run db:generate`       | Drizzle: turn schema changes into a new SQL migration in `drizzle/`.           |
| `bun run db:migrate:local`  | Apply migrations to the local (miniflare) D1.                                  |
| `bun run db:migrate:remote` | Apply migrations to the deployed D1.                                           |
| `bun run auth:generate`     | Regenerate the Better-Auth Drizzle tables into `src/server/db/auth-schema.ts`. |
| `bun run cf-typegen`        | Regenerate `worker-configuration.d.ts` from the bindings in `wrangler.jsonc`.  |
| `bun run generate-routes`   | Regenerate `src/routeTree.gen.ts` from `src/routes/`.                          |

## Testing

Three layers, all runnable from a clean checkout:

```bash
bun run test        # 41 files / 279 tests: unit (jsdom) + integration (workerd)
bun run test:e2e    # 10 Playwright flows against the built Worker
```

- **Unit** (`vitest --project unit`, jsdom) — scoring, time-zone and `.ics` formatting, edit
  tokens, Zod schemas, e-mail rendering, message-catalogue parity, and component behaviour
  via Testing Library. Files: `src/**/*.test.ts(x)`, `emails/**`, `messages/**`.
- **Integration** (`vitest --project workers`, `@cloudflare/vitest-plugin`) — anything named
  `*.workers.test.ts` runs inside real workerd with a real D1 (migrations applied per run)
  and real Durable Objects. This is where server functions, ownership guards, edit-token
  checks, digest batching and the deadline alarm are proven.
- **End-to-end** (Playwright, Chromium) — sign-up, sign-in, the full create → vote → edit →
  finalise → `.ics` flow, a live update landing in a second browser context, dashboard
  duplicate/delete and the locale switch. The suite builds the Worker and serves it with
  `vite preview`, because `vite dev` intercepts the WebSocket upgrade before it reaches
  workerd.

Running just one thing:

```bash
bunx vitest run src/lib/__tests__/scoring.test.ts
bunx vitest run --project workers src/do/__tests__/poll-room.workers.test.ts
bunx vitest run --project unit -t 'cycles null'                  # by test name
bunx playwright test e2e/poll-flow.spec.ts
bunx playwright test --ui                                        # interactive
```

Playwright needs a browser and its system libraries the first time:

```bash
bunx playwright install --with-deps chromium
```

The e2e suite seeds its data through `POST /api/test/seed`, which only answers when
`ENABLE_TEST_ROUTES=true` is present in `.dev.vars` **and** `APP_ENV` is not `production`.

## Project structure

```
src/
  server.ts               Worker entry: Paraglide middleware → TanStack Start, exports PollRoom
  start.ts                request middleware — CSP and the other security headers
  app.config.ts           name, tagline, brand colours, locales (rename the app here)
  router.tsx, routes/     file-based routes
    index.tsx             landing
    new.tsx               3-step poll creator
    p/$id/                poll page, edit page, calendar.ics
    dashboard.tsx settings.tsx login.tsx signup.tsx …
    api/                  auth/$, polls/$id/ws, test/seed
  components/
    ui/                   shadcn primitives, restyled
    poll/                 VoteGrid, VoteCell, Comments, ShareSheet, FinalizeDialog, …
    creator/              TypeStep, OptionsStep, SettingsStep, editors
    dashboard/ landing/ layout/ auth/
  server/
    db/                   Drizzle schema (app + Better-Auth tables), client
    auth/                 Better-Auth config, session/owner middleware, session server fns
    polls/                server functions, service layer, Zod schemas, view models
    mailer/               transport over the send_email binding, template rendering
    notifications/        typed Durable Object client, finalise e-mails
    http/                 Turnstile verification, rate limiting
  do/PollRoom.ts          sockets, digest buffer, alarms
  lib/                    scoring, time, ics, tokens, i18n, theme, motion helpers
  paraglide/              generated message functions (git-ignored)
emails/                   react-email templates
messages/en.json nb.json  the translation source of truth
drizzle/                  generated SQL migrations
e2e/                      Playwright specs and fixtures
test/                     shared Vitest setup, workers-pool wrangler config
public/                   logo.svg, favicon.svg
docs/screenshots/         README images (see `bun run screenshots`)
```

## Internationalisation

Strings live in [`messages/en.json`](./messages/en.json) and
[`messages/nb.json`](./messages/nb.json) and are compiled by
[Paraglide](https://inlang.com/m/gerre34r/library-inlang-paraglideJs) into tree-shakeable
functions (`m.poll_share_title()`). The active locale resolves from the `samla_locale`
cookie, then `Accept-Language`, then English, and is applied on the server too — so SSR
output and outgoing e-mails are localised, not just the client. A unit test fails the build
if the two catalogues drift apart in keys or placeholders.

To add a locale — say German:

1. Add `"de"` to `locales` in [`project.inlang/settings.json`](./project.inlang/settings.json)
   and to `appConfig.locales` in [`src/app.config.ts`](./src/app.config.ts).
2. Copy `messages/en.json` to `messages/de.json` and translate it, including the
   `locale_de` label used by the switcher.
3. Map it to a BCP-47 tag for `Intl` in `intlLocale()` in
   [`src/lib/i18n.ts`](./src/lib/i18n.ts).
4. Run `bun install` (the `postinstall` hook recompiles Paraglide) and `bun run test`.

The footer switcher renders one button per entry in `appConfig.locales`, so nothing else
needs touching.

## Roadmap

- **v1 — done.** Group date/time and free-text polls, organiser accounts (password, Google,
  passkeys), guest voting with later edits, comments, deadlines, finalising with `.ics`,
  live updates, e-mail notifications, English + Norwegian.
- **v2 — sign-up sheets.** Options gain a capacity; participants _claim_ a slot instead of
  voting on it. The `capacity` column already exists, unused, in the schema.
- **v3 — 1:1 booking pages.** An availability engine and Google Calendar sync, for
  "book 30 minutes with me" links.

Deliberately out of scope for now: hidden votes, invitee reminders, paid tiers, a
moderation console and magic-link sign-in.

## Contributing

Pull requests are welcome — see [CONTRIBUTING.md](./CONTRIBUTING.md). In short: the project
is developed test-first, commits follow
[Conventional Commits](https://www.conventionalcommits.org/), and
`bun run typecheck && bun run lint && bun run format:check && bun run test` should be green
before you open a PR.

## Security

Please **do not** open a public issue for a vulnerability. Report it privately through
[GitHub's advisory form](https://github.com/andersro93/scheduler/security/advisories/new)
or the address in [SECURITY.md](./.github/SECURITY.md), which also carries the
repository-settings checklist for the owner (branch protection, secret scanning, Dependabot
alerts, private vulnerability reporting) — those live in the GitHub UI, not in this repo.

## Notes

- **TypeScript is pinned to 5.9** until typescript-eslint supports TypeScript 7. The
  project otherwise tracks latest.
- **bun only.** There is no `package-lock.json`, and CI uses bun as well. `npm`/`pnpm` are
  not supported.
- **`src/paraglide/` is generated** and git-ignored; `bun install` recreates it.
- **The D1 id in `wrangler.jsonc` is a placeholder** (`00000000-…`) so a fresh clone can run
  locally. Replace it before deploying.

## Acknowledgements

Inspired by [Doodle](https://doodle.com/) and by [Rallly](https://rallly.co/), whose
open-source take on scheduling polls set the bar. Built on
[TanStack Start](https://tanstack.com/start), [Cloudflare Workers](https://workers.cloudflare.com/),
[Better Auth](https://www.better-auth.com/), [Drizzle](https://orm.drizzle.team/),
[Tailwind CSS](https://tailwindcss.com/) and [shadcn/ui](https://ui.shadcn.com/).

## Licence

[MIT](./LICENSE) © 2026 Anders Refsdal Olsen
