# samla — v1 design spec (Doodle/Rallly-style scheduling polls on Cloudflare Workers)

Date: 2026-08-20 · Working name: **samla** (Norwegian "gathered"; branding isolated in `src/app.config.ts` for a one-line rename)

## Context

Anders wants a free, public Doodle/Rallly-style app that runs **natively** on Cloudflare Workers with D1, using Better-Auth, a modern React full-stack framework, Tailwind, and a UI that is very fast and genuinely fun to use. This spec covers **v1**; the architecture deliberately leaves room for later stages, each of which gets its own spec → plan → build cycle:

| Stage | Scope |
|---|---|
| **v1 (this spec)** | Group date/time polls + generic option polls (one "poll" engine), organiser accounts, guest voting, comments, deadline, finalise, live updates, email notifications, en + nb |
| v2 | Sign-up sheets (options gain `capacity`; participants *claim* instead of vote) |
| v3 | 1:1 booking pages (availability engine, Google Calendar sync) |

## Decisions made (with the user)

- Public free product. Organisers need an account; voters are guests (name + optional email).
- Sign-in: **email + password** (verified email), **Google OAuth**, **passkeys**. No magic link.
- Poll features: yes / if-need-be / no voting; comments; guests can edit their own vote later; optional (or required) participant email.
- Organiser: finalise an option (+ .ics / add-to-calendar), voting deadline with countdown, dashboard, duplicate/delete.
- Notifications: organiser digest for new votes/comments (≤ 1 email / 10 min / poll); participants emailed on finalise; organiser emailed when deadline closes the poll.
- Delight: micro-interactions everywhere + rare celebration moments. Live updates via Durable Objects.
- Languages: English + Norwegian (bokmål) from day one.
- Stack: **TanStack Start** on Workers, **Drizzle + D1**, **Better-Auth (Drizzle adapter)**, **Durable Objects**, **Cloudflare Email Service**, Tailwind v4 + shadcn/ui + `motion`, Paraglide i18n, Vitest + Playwright. Package manager/runtime for tooling: **bun** (machine has bun 1.4.0, no node).
- Plan requirement: **Workers Paid ($5/mo)** — needed for Durable Objects and Email Service. Domain must be on Cloudflare DNS (Email Service).

## 1. Architecture

Single Worker, single deploy. `src/worker.ts` is a custom entry that exports the TanStack Start fetch handler as default and the `PollRoom` Durable Object class.

```
Browser ──SSR HTML / server fns──▶ Worker (TanStack Start, Vite + @cloudflare/vite-plugin)
   │                                 ├── D1 `DB`              source of truth (Drizzle ORM)
   │                                 ├── Better-Auth          sessions, email+pw, Google, passkeys
   │                                 ├── `EMAIL` send_email   Cloudflare Email Service
   │                                 ├── Turnstile, RATE_LIMITER binding
   └──WebSocket /api/polls/:id/ws──▶ PollRoom DO (idFromName(pollId), hibernating)
                                       ├── fan-out `poll.changed` to connected viewers; presence count
                                       └── alarms: organiser digest, deadline auto-close, email retry
```

Principles:
- **D1 owns all durable poll data.** Server functions write to D1 (atomic via `batch()`), then call the poll's DO best-effort. DO failure never fails a user request.
- **DO owns only ephemeral/coordination state**: connected sockets, pending digest events, scheduled alarm timestamps.
- Bindings accessed via `import { env } from "cloudflare:workers"` in server code.

Wrangler (`wrangler.jsonc`): `main: "src/worker.ts"`, `compatibility_flags: ["nodejs_compat"]`, `compatibility_date` = today, `observability.enabled: true`, bindings `DB` (d1), `POLL_ROOM` (durable object, SQLite-backed class with migration tag), `EMAIL` (send_email), `RATE_LIMITER` (ratelimit), secrets `BETTER_AUTH_SECRET`, `GOOGLE_CLIENT_ID/SECRET`, `TURNSTILE_SECRET_KEY`; vars `APP_URL`, `TURNSTILE_SITE_KEY`, `EMAIL_FROM`.

## 2. Data model (Drizzle schema → `drizzle-kit generate` → `wrangler d1 migrations apply`)

IDs: `nanoid(12)` url-safe for polls; `nanoid(16)` for other rows. Timestamps are ISO-8601 UTC strings (`text`).

- **`polls`**: `id` PK, `owner_id` FK→user, `type` `'datetime'|'options'`, `title`, `description?`, `location?`, `timezone` (IANA, organiser's; required for datetime polls), `status` `'open'|'closed'|'finalized'`, `deadline_at?`, `finalized_option_id?`, `require_participant_email` bool, `allow_comments` bool, `allow_if_need_be` bool, `notify_on_vote` bool, `notify_on_comment` bool, `created_at`, `updated_at`, `deleted_at?`. Index: `(owner_id, created_at)`.
- **`poll_options`**: `id` PK, `poll_id` FK, `position` int, `kind` `'date'|'datetime'|'text'`, `start_at?` (`YYYY-MM-DD` for `date`; UTC instant for `datetime`), `end_at?` (UTC instant, datetime slots with duration), `label?` (text options), `capacity?` int (null in v1, reserved). Index `(poll_id, position)`.
- **`participants`**: `id` PK, `poll_id` FK, `name`, `email?`, `user_id?` FK→user, `edit_token_hash?` (SHA-256 of a 32-byte random token; null for signed-in voters), `locale?`, `created_at`, `updated_at`. Index `(poll_id)`.
- **`votes`**: `participant_id` FK, `option_id` FK, `answer` `'yes'|'ifneedbe'|'no'`; PK `(participant_id, option_id)`. Missing row = no answer.
- **`comments`**: `id` PK, `poll_id` FK, `author_name`, `participant_id?`, `user_id?`, `body` (≤ 2000), `created_at`, `deleted_at?`. Index `(poll_id, created_at)`.
- Better-Auth tables (`user`, `session`, `account`, `verification`, `passkey`) generated by `bunx @better-auth/cli generate` into the same Drizzle schema file; `user` gets an extra `locale` column via `additionalFields`.

Derived, never stored: option score = `2·yes + 1·ifneedbe`; best option = highest score, tie → lowest `position`.

Deletion: polls are soft-deleted (`deleted_at`) and excluded from all public queries; options/participants/votes/comments carry `ON DELETE CASCADE` FKs so a future hard-purge is a single delete. Account deletion hard-deletes the user's polls (cascading). A scheduled hard-purge of soft-deleted polls is not part of v1 (see §11).

## 3. Routes and flows

| Route | Access | Purpose |
|---|---|---|
| `/` | public | Landing (value prop, CTA, language toggle) |
| `/new` | signed-in (else `/login?next=/new`) | 3-step creator: (1) type + title/description/location, (2) options — date picker with multi-select days and optional time slots (datetime polls) or a free-text list (option polls), (3) settings — deadline, allow if-need-be, allow comments, require participant email. Creates poll → `/p/:id` with share sheet open |
| `/p/:id` | public | Poll page: grid (options × participants), "add yourself" row, best-option highlight, comments, deadline countdown, finalised banner with *Add to calendar*, presence count. Owner additionally sees an admin bar: Finalise, Close/Reopen, Edit, Delete, Share, notification toggles |
| `/p/:id/edit` | owner | Edit details/options/settings. Removing an option with votes requires confirmation |
| `/p/:id/calendar.ics` | public | iCal for the finalised option (server route; 404 if not finalised) |
| `/dashboard` | signed-in | My polls: status, participant count, deadline, quick actions (open, duplicate, delete) |
| `/login`, `/signup`, `/forgot-password`, `/reset-password`, `/verify-email` | public | Better-Auth UI incl. Turnstile, Google button, passkey sign-in |
| `/settings` | signed-in | Name, passkeys (add/remove), language, delete account |
| `/api/auth/*` | — | Better-Auth handler (server route) |
| `/api/polls/:id/ws` | public | WebSocket upgrade forwarded to the poll's DO |

**Server functions** (TanStack `createServerFn`, Zod 4 input schemas shared with the client, in `src/server/polls/*`):
`createPoll`, `updatePoll`, `deletePoll`, `duplicatePoll`, `closePoll`/`reopenPoll`, `finalizePoll(optionId)`, `getPoll(id)` (public view model: hides emails and tokens), `listMyPolls`, `addParticipant(name, email?, answers[], turnstileToken)`, `updateParticipant(participantId, editToken|session, name?, answers[])`, `removeParticipant` (owner), `addComment(body, authorName, turnstileToken)`, `deleteComment` (owner or author), `updateNotificationPrefs`.

**Guest vote flow**: fill name (+ email if required), tap cells, submit → server verifies Turnstile + rate limit + poll `open`, inserts participant + votes in one batch, returns `{participantId, editToken}` → client stores `samla:edit:<pollId>` in localStorage → row shows as "You" with an Edit control. Signed-in voters: `user_id` set, no token, editable from any device.

**Finalise**: owner picks an option (default: best) → `status='finalized'`, `finalized_option_id`, DO broadcast, emails to every participant with an email + owner, `.ics` attached and linked.

**Deadline**: UTC instant; DO alarm → `status='closed'`, broadcast, owner emailed. Owner can still finalise/reopen. Countdown rendered client-side from ISO value (no server clock dependence).

## 4. Auth (Better-Auth 1.7)

```ts
betterAuth({
  database: drizzleAdapter(db, { provider: "sqlite" }),   // db = drizzle(env.DB) from drizzle-orm/d1
  emailAndPassword: { enabled: true, requireEmailVerification: true, sendResetPassword, },
  emailVerification: { sendVerificationEmail, sendOnSignUp: true },
  socialProviders: { google: { clientId, clientSecret } },
  plugins: [passkey({ rpID, rpName, origin }), captcha({ provider: "cloudflare-turnstile", secretKey })],
  user: { additionalFields: { locale: { type: "string", required: false } } },
})
```
- `provider: "sqlite"` is the dialect; D1 is SQLite. Drizzle adapter chosen (over native `database: env.DB`) so auth + app tables share one schema and one migration pipeline.
- Client: `createAuthClient({ plugins: [passkeyClient()] })`. Route guards in `beforeLoad` using a per-request cached `getSession` server fn.
- Authorization helpers in `src/server/auth/guards.ts`: `requireSession()`, `requireOwner(pollId)`, `verifyEditToken(participantId, token)`.

## 5. Live updates & notifications (PollRoom DO)

- **Sockets**: hibernation API (`acceptWebSocket`, `webSocketMessage/Close`). Client connects on mount of `/p/:id`, reconnects with backoff. Server→client messages: `{type:'poll.changed', entity:'participant'|'vote'|'comment'|'poll'}` and `{type:'presence', count}`. Client handler invalidates the TanStack Query `['poll', id]` → refetch (always consistent with D1; no delta bookkeeping).
- **RPC from server fns** (DO RPC methods): `broadcast(event)`, `enqueueDigest(item)`, `syncDeadline(deadlineAt|null)`.
- **Digest**: `enqueueDigest` appends to DO storage `digest:items[]`; if no `digestAt`, set `digestAt = now+10min`; re-arm alarm to `min(digestAt, deadlineAt, retryAt)`. On alarm with digest due: load poll + owner from D1, skip if flags off or poll deleted, send one email summarising counts + names, clear. Failure → `retryAt = now+5min`, max 3 attempts, then drop + log.
- **Deadline**: `syncDeadline` stores and re-arms. On fire: if poll still `open` and `deadline_at` unchanged, set `closed`, broadcast `poll`, email owner.
- **Mailer** (`src/server/mailer/`): `send({to, subject, react, locale})` renders `react-email` → html+text, calls `env.EMAIL.send({from: EMAIL_FROM, …})`. Templates: verify-email, reset-password, organiser-digest, poll-finalized (with `.ics` attachment), poll-closed. Localised via Paraglide messages using the recipient's locale (participant/user `locale`, else poll owner's, else `en`).

## 6. UI, design system, delight, i18n

- Tailwind v4 CSS-first tokens in `src/app.css`; shadcn/ui primitives (button, dialog, popover, tooltip, toast, dropdown, calendar, input, switch, tabs) added selectively and restyled: one signature accent, display + text typefaces (self-hosted, `font-display: swap`), rounded-but-not-bubbly radii, generous spacing, dark mode via `prefers-color-scheme` + toggle.
- `src/app.config.ts`: name, tagline, accent, support email, URLs.
- **Micro-interactions (`motion`)**: vote cell `whileTap` spring + icon morph cycling yes→if-need-be→no; rows/comments enter with `layout` animation; best-option column glow that moves with score; pressed-depth buttons; presence dots pulse on join.
- **Celebration**: `canvas-confetti` on Finalise (owner) and a small burst on a guest's first submit; finalised banner slides in. All gated behind `prefers-reduced-motion`.
- **Speed**: SSR poll page renders the full grid without client JS (progressive enhancement); hydrate for interactivity; no images on critical path; code-split creator and auth routes. Target LCP < 1 s on 4G for `/p/:id`.
- **Time zones**: datetime options stored UTC + organiser tz; rendered in viewer's tz via `Intl` with a "shown in <tz> · change" control; `date` options are tz-less. `date-fns` + `Intl` only.
- **i18n**: Paraglide JS 2 (`messages/en.json`, `messages/nb.json`), strategy cookie → `Accept-Language` → `en`; no URL prefix; switcher in footer + settings; server-side locale set per request for SSR + emails.

## 7. Security, abuse, errors

- Turnstile on sign-up/sign-in/forgot (captcha plugin) and on `addParticipant`/`addComment` (verified server-side). `RATE_LIMITER` per IP: create 10/10 min, vote 30/10 min, comment 20/10 min. Unguessable poll IDs; hashed edit tokens; every mutation re-checks owner/token and poll status server-side. Zod caps: title ≤ 200, description ≤ 2000, location ≤ 200, options ≤ 100, participants ≤ 500/poll, name ≤ 80, comment ≤ 2000.
- Headers: strict CSP (self + Turnstile), `HttpOnly`/`Secure`/`SameSite=Lax` session cookie (Better-Auth default), HSTS.
- Errors: validation → field errors inline; closed/finalised → 409 → friendly "voting has closed" state; not found → 404 page; DO/email failures logged, never surfaced as request failures.

## 8. Testing

- **Unit (Vitest 4)**: scoring/best option, tz + ics formatting, token hash/verify, Zod schemas, digest text.
- **Integration (`@cloudflare/vitest-pool-workers`, real workerd + D1 + DO)**: create → vote → finalise path; owner-only guards; edit-token guard; digest batching (two enqueues → one email via alarm); deadline alarm closes poll; mailer called with expected payload (binding mocked).
- **E2E (Playwright)**: sign up (Turnstile test keys) → create datetime poll → guest votes in second browser context → live update appears in first → finalise → banner + `.ics` downloads; `nb` locale smoke.
- CI: `bun run typecheck && bun run test && bun run test:e2e` on push; deploy via `wrangler deploy` on `main`.

## 9. Project layout (single package, bun)

```
src/
  worker.ts                 custom entry: export default start handler; export { PollRoom }
  app.config.ts             brand/working-name config
  app.css                   Tailwind v4 tokens
  router.tsx, routes/       file-based routes (__root, index, new, p.$id, p.$id.edit, dashboard, auth/*, settings, api/*)
  components/ui/            shadcn primitives
  components/poll/          VoteGrid, VoteCell, ParticipantRow, OptionHeader, BestBadge, Comments, ShareSheet, FinalizeDialog, DeadlineCountdown
  components/creator/       TypeStep, OptionsStep (DatePicker, TimeSlots, TextOptions), SettingsStep
  server/db/                schema.ts, client.ts
  server/auth/              auth.ts (betterAuth), client.ts, guards.ts
  server/polls/             queries.ts, mutations.ts (server fns), viewmodel.ts, schemas.ts (zod)
  server/mailer/            mailer.ts
  server/notifications/     do-client.ts (typed stub helpers)
  do/PollRoom.ts
  lib/                      scoring.ts, time.ts, ics.ts, tokens.ts, i18n.ts (paraglide runtime glue)
emails/                     react-email templates
messages/en.json, nb.json
drizzle/                    migrations
e2e/                        playwright
wrangler.jsonc, drizzle.config.ts, vite.config.ts, vitest.config.ts, playwright.config.ts
```

## 10. Pinned versions (latest as of 2026-08-20, verified on the npm registry)

@tanstack/react-start 1.168.48 · @tanstack/react-router 1.170.31 · @tanstack/react-query 5.101.4 · vite 8.2.2 · @vitejs/plugin-react 6.1.0 · @cloudflare/vite-plugin 1.53.1 · wrangler 4.125.0 · @cloudflare/workers-types 5.20260820.1 · react / react-dom 19.2.8 · better-auth / @better-auth/drizzle-adapter / @better-auth/passkey 1.7.1 · drizzle-orm 0.45.2 · drizzle-kit 0.31.10 · tailwindcss / @tailwindcss/vite 4.3.3 · shadcn 4.18.0 · motion 13.1.1 · canvas-confetti 1.9.4 · @inlang/paraglide-js 2.24.1 · zod 4.4.3 · @react-email/components 1.0.12 · @react-email/render 2.1.0 · @marsidev/react-turnstile 1.6.0 · nanoid 6.0.1 · date-fns 4.4.0 · vitest 4.1.11 · @cloudflare/vitest-pool-workers 0.22.0 · @playwright/test 1.62.1 · typescript 7.0.2 · bun 1.4.0.

Known risks with "latest": TypeScript 7 is the Go-native compiler — if any tool in the chain rejects it, fall back to 5.9.x and record why. Cloudflare Email Service is public beta. Wrangler/Vite under bun (no node installed) generally works; if workerd/Playwright require node, install node via `fnm` and note it in the README.

## 10b. Repository, README, GitHub hygiene

- Repo: **https://github.com/andersro93/scheduler** (public). Default branch `main`; work on feature branches merged via PR.
- **README.md** must look professional: logo/wordmark header, one-line pitch, screenshots/GIF placeholders (replaced once UI exists), feature list, architecture diagram (mermaid), quick start (`bun install`, `bun run dev`), Cloudflare setup (D1 create, migrations, secrets, Email Service domain verification, Turnstile keys, Google OAuth), scripts table, testing section, project structure, roadmap (v2 sign-up sheets, v3 1:1 booking), contributing, security, licence badges (CI, CodeQL, licence).
- **License**: MIT (assumption — easy to switch to AGPL-3.0 if the user prefers copyleft like Rallly).
- **`.github/`**: `dependabot.yml` (npm weekly grouped minor/patch; github-actions weekly), `workflows/ci.yml` (bun install, typecheck, lint, unit + workers tests, Playwright e2e with artefacts), `workflows/codeql.yml` (javascript-typescript, on push/PR/weekly), `workflows/deploy.yml` (wrangler deploy on `main` with `CLOUDFLARE_API_TOKEN`; applies D1 migrations first), `SECURITY.md` (private reporting via GitHub advisories), `PULL_REQUEST_TEMPLATE.md`, `ISSUE_TEMPLATE/` (bug, feature), `CODEOWNERS`. Also `CONTRIBUTING.md`, `.editorconfig`, `.nvmrc`-equivalent `.bun-version`.
- Repo settings that can't live in files (branch protection, secret scanning, Dependabot alerts, private vulnerability reporting) are listed in README/SECURITY for the owner to enable in the GitHub UI.

## 10c. Test coverage expectations

"Lots of tests": every server function gets integration tests for the happy path **and** each guard (not owner, bad token, closed poll, Turnstile fail, rate limited, over limits); every `lib/` module gets unit tests; DO behaviour (broadcast, digest batching, deadline, retry) tested in the workers pool; UI components with behaviour (VoteCell cycle, DeadlineCountdown, ShareSheet copy) get Vitest + Testing Library tests; Playwright covers the end-to-end flows in §8 plus auth flows (sign-up/verify, password reset, passkey registration where Playwright's virtual authenticator allows). CI fails on any test failure; coverage report uploaded as artefact.

## 11. Out of scope for v1 (explicit)

Hidden votes/participants, vote reminders to invitees, deadline-approaching reminders, sign-up capacity, 1:1 booking, calendar sync, paid tiers, admin/moderation console, hard-purge job for soft-deleted polls, magic-link sign-in.

