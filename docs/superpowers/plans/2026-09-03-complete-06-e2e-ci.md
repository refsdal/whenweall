# Playwright Coverage, Harness Hardening & Image-Mode CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Playwright suite exercise every feature plans A–E restored (e-mail flows via Mailpit, comments, signed-in voting, the creator wizard's text/time-slot polls, poll editing and deadlines, booking-page creation and rescheduling, invitations with the org switcher, settings, admin lock/unlock/delete and the jobs page), harden the harness (port 3100, Turnstile off, pinned Mailpit, marker-based teardown, hard failures instead of skips), and add a CI job that runs the same suite against the built Docker image under compose's hardening flags.

**Architecture:** The suite keeps its shape — `playwright.config.ts` → `webServer: bash e2e/run-server.sh` → real Go binary serving the real built SPA, Postgres and Mailpit as throwaway containers, `POST /api/test/seed` for fixtures. Three additions: (1) `e2e/mailpit.ts` polls Mailpit's HTTP API (`GET /api/v1/search?query=to:"<addr>"`, `GET /api/v1/message/{ID}`) so specs can follow emailed links; (2) `run-server.sh` gains an `E2E_SERVER=image` branch that only waits for an already-running compose stack (`compose.e2e.yaml` overlay + `e2e/compose.e2e.env`) and writes a marker file so `global-teardown.ts` only removes what it started; (3) the seed route grows `failedJob: true` (inserts a dead-lettered `scheduled_jobs` row), forwards `name`, marks the user verified and signs in explicitly so it survives plan A's verification gate.

**Tech Stack:** Playwright 1.62 (`@playwright/test`, bun-run from the root `package.json`), Go 1.26 (`internal/httpserver/testroutes.go` + `internal/testdb`), Docker CLI, Docker Compose v2 (overlay files, `--env-file`, `up --wait`), Mailpit `axllent/mailpit:v1.31.0` (tag verified with `docker manifest inspect`), Postgres `postgres:18-alpine`, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` §9 ("Playwright e2e is the oracle… runs in CI against the real built Docker image + Postgres") plus the Plan F brief (`e2e + CI`, decisions fixed: Turnstile off in e2e, port 3100, marker-based teardown, pinned Mailpit, `mailpit.ts`, CI `e2e-image` job with a `compose.e2e.yaml` overlay and `E2E_SERVER=image`). Findings this plan closes: "Every e-mail flow has zero coverage", "Organization invitations lost their only e2e coverage", "Comments (post + delete) have no e2e coverage", "Signed-in voting is never exercised", "Text polls and datetime-slot creation through the wizard are not covered", "Editing a published poll and the voting deadline are not covered", "Booking page creation/editing via the UI is never exercised", "reuseExistingServer + fixed port 3000 … self-destructive", "`test.skip` guards on seed results", "Several tests assert too little", "CI e2e runs `go run` against the source tree, not the built Docker image".

## Global Constraints

- Branch `feat/go-rewrite`; every task lands as its own commit. Commit messages are conventional (`test(e2e): …`, `feat(testroutes): …`, `ci: …`, `docs: …`) and END with exactly these two trailer lines:
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`
  `Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p`
- The e2e app port is **3100** (`APP_PORT` in `e2e/e2e-env.ts`; `APP_URL=http://localhost:3100` is what the Go server puts into e-mailed links in both server modes).
- Turnstile is **OFF** in e2e: `TURNSTILE_SITE_KEY`/`TURNSTILE_SECRET_KEY` are never set for the e2e server; `waitForTurnstile` is deleted; no spec references `cf-turnstile-response`. Server-side captcha stays covered by `internal/httpserver/turnstile_test.go` (plan A).
- Mailpit is pinned to `axllent/mailpit:v1.31.0` (verified: `docker manifest inspect axllent/mailpit:v1.31.0` succeeds; it is the newest release tag on github.com/axllent/mailpit/releases at planning time). Postgres stays `postgres:18-alpine`.
- Mailpit HTTP API (verified against the project's `swagger.json`): `GET /api/v1/search?query=<q>&limit=<n>` → `{"messages":[{"ID","MessageID","From":{Name,Address},"To":[{Name,Address}],"Subject","Snippet","Created",…}],…}` newest first; `GET /api/v1/message/{ID}` → `{"ID","Subject","From","To","Text","HTML",…}`; `DELETE /api/v1/messages` body `{"IDs":[…]}`. Search syntax `to:"addr@example.com"` — **verify at run time** on first use (Task 5 step 4).
- Teardown removes only what the run started: `run-server.sh` writes `e2e/.run-server-marker.json` (gitignored) listing the containers it created and whether it touched `internal/httpserver/dist`; `global-teardown.ts` acts only on that marker.
- `E2E_SERVER=image` selects image mode: the compose stack (`compose.yaml` + `compose.e2e.yaml`, env from `e2e/compose.e2e.env`, project name `whenweall-e2e`) is already up on :3100; `reuseExistingServer` is true in that mode and `run-server.sh` only waits for `/healthz`.
- Seed results are hard failures: a fixture whose `pollId`/`pageId`/`handle`/`slug`/`failedJobId` is missing throws inside the fixture; no `test.skip` on seed results anywhere.
- Locators: prefer `getByRole`/`getByLabel`/`getByTestId`; assert with auto-waiting `expect(...).toBeVisible()`/`toHaveCount()`; poll Mailpit with `expect.poll`; **no fixed `waitForTimeout` sleeps**. Realtime assertions use `{ timeout: 10_000 }`; mail assertions `{ timeout: 30_000 }` (the jobs worker polls every 5 s and is woken by `NOTIFY`).
- Every new user-facing string this plan touches already exists in `web/messages/en.json`; this plan adds NO message keys (it only consumes plans A–E's UI). Where a plan left a label open, the spec uses a role + regex locator and the assumption is stated inline in a code comment.
- Gates before declaring the plan done: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`; `cd web && bun run typecheck && bun run lint && bunx vitest run`; `bunx playwright test` (go mode); `E2E_SERVER=image bunx playwright test` after `e2e/compose-e2e.sh up -d --wait`.

## Dependency note on task order

The brief lists "seed-route extension" after the new specs, but `admin.spec.ts` (Task 13) needs `failedJob: true`, `vote-signed-in.spec.ts` (Task 7) needs the seeded display name, and every fixture needs seeded users to pass plan A's verification gate — so the Go seed work is **Task 4**, right after the harness tasks, and the specs follow.

## File map

| Path | Responsibility |
| --- | --- |
| `e2e/e2e-env.ts` (modify) | Ports, images, server mode, marker path, `APP_URL` |
| `playwright.config.ts` (modify) | Base URL from `APP_PORT`; mode-aware `reuseExistingServer`; no Turnstile env |
| `e2e/fixtures.ts` (modify) | Seeded-user fixtures (typed, hard-fail), `signIn`, calendar helpers, no Turnstile |
| `e2e/run-server.sh` (modify) | go mode: containers + build + `go run`, writes marker; image mode: wait for `/healthz` |
| `e2e/global-teardown.ts` (modify) | Marker-based cleanup only |
| `e2e/mailpit.ts` (create) | `searchMail`, `countMail`, `waitForMail`, `readMail`, `extractLinks`, `extractLink` |
| `e2e/auth-email.spec.ts`, `comments.spec.ts`, `vote-signed-in.spec.ts`, `creator.spec.ts`, `poll-edit.spec.ts`, `booking-create.spec.ts`, `invitations.spec.ts`, `settings.spec.ts` (create) | One journey per file |
| `e2e/admin.spec.ts`, `auth.spec.ts`, `dashboard.spec.ts`, `poll-flow.spec.ts`, `booking.spec.ts`, `live.spec.ts`, `mobile.spec.ts`, `signup.spec.ts`, `screenshots.spec.ts` (modify) | Turnstile removal, hard-fail guards, stronger assertions |
| `internal/httpserver/testroutes.go` + `testroutes_test.go` (modify), `cmd/whenweall/main.go` (modify) | Seed route: `failedJob`, `name`, verified + explicit sign-in |
| `compose.e2e.yaml`, `e2e/compose.e2e.env`, `e2e/compose-e2e.sh`, `e2e/assert-hardening.sh` (create) | Image-mode stack |
| `.github/workflows/ci.yml` (modify) | New `e2e-image` job |
| `.gitignore`, `README.md`, `CONTRIBUTING.md` (modify) | Marker ignore; docs |

---

### Task 1: Harness constants, config and fixtures — port 3100, Turnstile off, typed hard-fail fixtures

**Files:**
- Modify: `e2e/e2e-env.ts` (whole file)
- Modify: `playwright.config.ts` (whole file)
- Modify: `e2e/fixtures.ts` (whole file)
- Modify: `e2e/auth.spec.ts:1,11,32`, `e2e/booking.spec.ts:1-8,63`, `e2e/live.spec.ts:1,34`, `e2e/mobile.spec.ts:1,61`, `e2e/poll-flow.spec.ts:1-8,60`, `e2e/signup.spec.ts:1,19`, `e2e/screenshots.spec.ts:2-10,81,155`

**Interfaces:**
- Consumes: nothing new (plan A made `TurnstileField` render null and every submit gate `captchaEnabled && !captchaToken` when `publicConfig.turnstileSiteKey` is empty).
- Produces (for every later task): from `e2e/e2e-env.ts` — `APP_PORT = 3100`, `APP_URL`, `MAILPIT_IMAGE`, `DB_IMAGE`, `MAILPIT_HTTP_PORT = 8026`, `SERVER_MODE: 'go' | 'image'`, `RUN_MARKER`, `AUTH_SECRET`; from `e2e/fixtures.ts` — `test`, `expect`, `type SeededUser`, fixtures `user`, `userWithPoll: SeededUser & { pollId: string }`, `userWithSignup: SeededUser & { pollId: string }`, `userWithBookingPage: SeededUser & { pageId: string; handle: string; slug: string }`, `userStaff`, helpers `signIn(page, {email,password}, {next?})`, `waitForHydration(page)`, `pickTwoCalendarDays(page)`, `pickFirstEnabledDay(scope: Page | Locator)`, `pickFirstEnabledDayNotToday(scope: Page | Locator)`.

- [ ] **Step 1: Replace `e2e/e2e-env.ts`**

```ts
/**
 * Constants shared between playwright.config.ts's `webServer` (its `run-server.sh` starts the
 * throwaway Postgres/Mailpit containers), `global-teardown.ts` (stops them), `mailpit.ts` (reads
 * the inbox) and the specs — one place so container names, ports, images and credentials can't
 * drift apart between the files that need them.
 *
 * Ports are deliberately different from compose.yaml's own dev/smoke ports (3000/5433/1025/8025),
 * so this suite can run alongside a `docker compose up` session without colliding — including the
 * app port: on 3000, `reuseExistingServer` would silently point the suite at the compose app,
 * which has no seed route.
 */
export const DB_CONTAINER = 'whenweall-e2e-db'
export const DB_IMAGE = 'postgres:18-alpine'
export const DB_PORT = 5434
export const DB_USER = 'whenweall'
export const DB_PASSWORD = 'e2e-test-password'
export const DB_NAME = 'whenweall'

export const MAILPIT_CONTAINER = 'whenweall-e2e-mailpit'
/**
 * Pinned, not `:latest`: a Mailpit release that changed the /api/v1 search or message shape would
 * otherwise break `mailpit.ts` on an unrelated day. Bump deliberately, after
 * `docker manifest inspect axllent/mailpit:<tag>` confirms the tag exists. Duplicated in
 * compose.e2e.yaml (compose can't read TypeScript) — keep the two in sync.
 */
export const MAILPIT_IMAGE = 'axllent/mailpit:v1.31.0'
export const MAILPIT_SMTP_PORT = 1026
export const MAILPIT_HTTP_PORT = 8026

export const APP_PORT = 3100
/** What the Go server puts into every e-mailed link (APP_URL) in both server modes. */
export const APP_URL = `http://localhost:${APP_PORT}`

/**
 * `go` (default): run-server.sh builds the SPA and `go run`s the server against two `docker run`
 * containers. `image`: the built Docker image is already up on APP_PORT via the compose overlay
 * (`e2e/compose-e2e.sh up -d --wait`) and the suite only waits for it.
 */
export type ServerMode = 'go' | 'image'
export const SERVER_MODE: ServerMode = process.env.E2E_SERVER === 'image' ? 'image' : 'go'

/**
 * Written by run-server.sh when — and only when — IT started containers / overwrote dist; read and
 * removed by global-teardown.ts, so a developer-started server that Playwright merely reused is
 * never torn down under them.
 */
export const RUN_MARKER = 'e2e/.run-server-marker.json'

/**
 * Not a real secret — a fixed, throwaway value for a database that lives for one test run.
 * Duplicated in e2e/compose.e2e.env for image mode; keep the two in sync.
 */
export const AUTH_SECRET = 'e2e-not-a-real-secret-YuerLHH9iaTZviHi0y2hkpP'
```

- [ ] **Step 2: Replace `playwright.config.ts`**

```ts
import { defineConfig, devices } from '@playwright/test'
import {
  APP_PORT,
  APP_URL,
  AUTH_SECRET,
  DB_CONTAINER,
  DB_IMAGE,
  DB_NAME,
  DB_PASSWORD,
  DB_PORT,
  DB_USER,
  MAILPIT_CONTAINER,
  MAILPIT_HTTP_PORT,
  MAILPIT_IMAGE,
  MAILPIT_SMTP_PORT,
  RUN_MARKER,
  SERVER_MODE,
} from './e2e/e2e-env'

/**
 * The suite runs the real Go backend (cmd/whenweall) with the SPA it serves built and copied into
 * its embedded dist/ — see `webServer.command` below — rather than a dev server: `live.spec.ts`
 * and `booking.spec.ts` need a real WebSocket upgrade the way a production deployment actually
 * handles it, and the built SPA is what internal/httpserver/spa.go actually serves in every real
 * environment (see Dockerfile's own `web` build stage).
 *
 * Two server modes, picked by `E2E_SERVER` (see e2e/e2e-env.ts):
 *   - `go` (default): `e2e/run-server.sh` starts Postgres + Mailpit as two throwaway `docker run`
 *     containers, builds the SPA, and `go run`s the server on :3100. `global-teardown.ts` removes
 *     exactly the containers that script recorded in its marker file, nothing else.
 *   - `image`: the built Docker image is already running via `e2e/compose-e2e.sh up -d --wait`
 *     (compose.yaml + compose.e2e.yaml — read_only, cap_drop ALL, no-new-privileges, user 65532).
 *     Playwright reuses that server; run-server.sh only waits for /healthz if it is ever invoked.
 *
 * Turnstile is deliberately NOT configured here: the suite covers the documented default (captcha
 * off) and no longer depends on challenges.cloudflare.com. The server-side verifier is unit-tested
 * in internal/httpserver/turnstile_test.go.
 */

/**
 * `screenshots.spec.ts` only captures the marketing images in `docs/screenshots/` — it asserts
 * almost nothing and writes files into the working tree, so it stays out of the regular suite
 * and of CI. `bun run screenshots` sets `SCREENSHOTS=1` to opt it back in.
 */
const captureScreenshots = process.env.SCREENSHOTS === '1'

const databaseURL = `postgres://${DB_USER}:${DB_PASSWORD}@localhost:${DB_PORT}/${DB_NAME}?sslmode=disable`

export default defineConfig({
  testDir: 'e2e',
  testIgnore: captureScreenshots ? undefined : '**/screenshots.spec.ts',
  timeout: 60_000,
  // Capped rather than Playwright's own CPU-count default: every worker's browser contexts all
  // hit the SAME one Go server process and its one DATABASE_POOL_SIZE-bounded connection pool
  // (unlike a real deployment, this suite has no fleet of replicas to spread load across) — on a
  // many-core machine, the default degrades into real contention (observed: a session-resolving
  // query failing with "context canceled" under the full suite's own parallelism, an
  // infrastructure flake, not a product bug — auth.spec.ts's dashboard-loads assertion, gone at
  // --workers=4). 4 is a deliberate, generous-but-bounded number, not a magic one — raise it
  // alongside DATABASE_POOL_SIZE below if the suite grows enough to need more parallelism.
  workers: process.env.CI ? 2 : 4,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  // A fixed browser locale keeps Paraglide's `preferredLanguage` strategy deterministic: without
  // it the suite resolves whatever `Accept-Language` the host machine happens to send, and the
  // English assertions in `i18n.spec.ts` pass or fail depending on the developer's OS settings.
  use: { baseURL: APP_URL, locale: 'en-US', trace: 'on-first-retry' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  globalTeardown: './e2e/global-teardown.ts',
  webServer: {
    command: 'bash e2e/run-server.sh',
    url: `${APP_URL}/healthz`,
    // Locally a developer may keep `bash e2e/run-server.sh` running and iterate; in image mode the
    // compose stack IS the server and Playwright must not try to start another one on the same
    // port (it would refuse with "already used" otherwise).
    reuseExistingServer: !process.env.CI || SERVER_MODE === 'image',
    timeout: 180_000,
    env: {
      // Read by e2e/run-server.sh to pick its branch, start/probe the throwaway containers and
      // record what it started.
      E2E_SERVER: SERVER_MODE,
      APP_PORT: String(APP_PORT),
      RUN_MARKER,
      DB_CONTAINER,
      DB_IMAGE,
      DB_PORT: String(DB_PORT),
      DB_USER,
      DB_PASSWORD,
      DB_NAME,
      MAILPIT_CONTAINER,
      MAILPIT_IMAGE,
      MAILPIT_SMTP_PORT: String(MAILPIT_SMTP_PORT),
      MAILPIT_HTTP_PORT: String(MAILPIT_HTTP_PORT),
      // Read by cmd/whenweall itself (internal/config.Load).
      ENABLE_TEST_ROUTES: 'true',
      APP_ENV: 'test',
      APP_URL,
      PORT: String(APP_PORT),
      DATABASE_URL: databaseURL,
      DATABASE_POOL_SIZE: '10',
      AUTH_SECRET,
      SMTP_HOST: 'localhost',
      SMTP_PORT: String(MAILPIT_SMTP_PORT),
      SMTP_SECURE: 'false',
      EMAIL_FROM: 'whenweall e2e <no-reply@localhost>',
      TRUST_PROXY: 'false',
      MIGRATE_ON_BOOT: 'true',
    },
  },
})
```

- [ ] **Step 3: Replace `e2e/fixtures.ts`**

```ts
import {
  test as base,
  expect,
  type APIRequestContext,
  type Locator,
  type Page,
} from '@playwright/test'

/** What `POST /api/test/seed` (internal/httpserver/testroutes.go) hands back for a freshly created,
 * already-verified user. Optional fields are `null` in the JSON unless the matching `with*` flag was
 * sent — the typed fixtures below turn a missing one into a hard failure. */
export type SeededUser = {
  email: string
  password: string
  name: string
  pollId?: string | null
  pageId?: string | null
  handle?: string | null
  slug?: string | null
  failedJobId?: string | null
}

export type SeedOptions = {
  name?: string
  withPoll?: boolean
  withSignup?: boolean
  withBookingPage?: boolean
  role?: 'staff'
  /** Also insert one dead-lettered `scheduled_jobs` row (attempts == max_attempts) and return its id. */
  failedJob?: boolean
}

async function seed(request: APIRequestContext, opts: SeedOptions = {}): Promise<SeededUser> {
  const response = await request.post('/api/test/seed', {
    data: {
      name: opts.name ?? 'E2E User',
      withPoll: opts.withPoll ?? false,
      withSignup: opts.withSignup ?? false,
      withBookingPage: opts.withBookingPage ?? false,
      failedJob: opts.failedJob ?? false,
      ...(opts.role ? { role: opts.role } : {}),
    },
  })
  if (!response.ok()) {
    throw new Error(
      `POST /api/test/seed responded ${response.status()}: ${await response.text()} — the e2e server ` +
        `must run with ENABLE_TEST_ROUTES=true (playwright.config.ts webServer.env / e2e/compose.e2e.yaml)`,
    )
  }
  return (await response.json()) as SeededUser
}

/** Narrows a seed result: the named fields must be non-empty strings, else the fixture fails loudly
 * (a `test.skip` here would silently hide a broken seed route behind a green run). */
function requireFields<K extends keyof SeededUser>(
  seeded: SeededUser,
  keys: K[],
): SeededUser & { [P in K]: string } {
  for (const key of keys) {
    if (typeof seeded[key] !== 'string' || seeded[key] === '') {
      throw new Error(`POST /api/test/seed did not return "${String(key)}": ${JSON.stringify(seeded)}`)
    }
  }
  return seeded as SeededUser & { [P in K]: string }
}

type Fixtures = {
  /** A verified user with no polls of their own. */
  user: SeededUser
  /** A verified user that already owns one seeded two-option datetime poll ("Seeded test poll",
   * Europe/Oslo, comments allowed — internal/polls/seed.go). */
  userWithPoll: SeededUser & { pollId: string }
  /** A verified user that already owns one seeded sign-up sheet (Slot 1 capacity 1, Slot 2 unlimited). */
  userWithSignup: SeededUser & { pollId: string }
  /**
   * A verified user with a handle and one seeded booking page: weekday 09:00–17:00 Europe/Oslo,
   * 30-minute slots, slug `intro-call` — see `CreateSampleBookingPage` in internal/bookings/seed.go.
   */
  userWithBookingPage: SeededUser & { pageId: string; handle: string; slug: string }
  /** A verified user carrying the platform staff role, for the admin console. */
  userStaff: SeededUser
  /** A staff user plus one dead-lettered job the console's Jobs page can list and retry. */
  userStaffWithFailedJob: SeededUser & { failedJobId: string }
}

/**
 * Every fixture seeds its own user via the test-only `/api/test/seed` route (only mounted when the
 * server runs with `ENABLE_TEST_ROUTES=true`, which config.Load refuses alongside
 * APP_ENV=production), so specs never share state and can run in any order or in parallel.
 */
// The fixture callback's second parameter is named `provide` rather than Playwright's usual
// `use` so `eslint-plugin-react-hooks` doesn't mistake `use(...)` for a React hook call — this
// file has no React in it.
export const test = base.extend<Fixtures>({
  user: async ({ request }, provide) => {
    await provide(await seed(request))
  },
  userWithPoll: async ({ request }, provide) => {
    await provide(requireFields(await seed(request, { withPoll: true }), ['pollId']))
  },
  userWithSignup: async ({ request }, provide) => {
    await provide(requireFields(await seed(request, { withSignup: true }), ['pollId']))
  },
  userWithBookingPage: async ({ request }, provide) => {
    await provide(
      requireFields(await seed(request, { withBookingPage: true }), ['pageId', 'handle', 'slug']),
    )
  },
  userStaff: async ({ request }, provide) => {
    await provide(await seed(request, { role: 'staff' }))
  },
  userStaffWithFailedJob: async ({ request }, provide) => {
    await provide(
      requireFields(await seed(request, { role: 'staff', failedJob: true }), ['failedJobId']),
    )
  },
})

export { expect }

/**
 * Waits until the React tree has mounted (the root layout sets `data-hydrated` on <html> after
 * mount — web/src/routes/__root.tsx). Typing into a controlled input before that point is silently
 * discarded when React attaches, which is the classic source of "I filled it but it's empty" flakes.
 */
export async function waitForHydration(page: Page): Promise<void> {
  await page.locator('html[data-hydrated="true"]').waitFor({ state: 'attached', timeout: 15_000 })
}

/**
 * Signs in through the real login form (fills the fields, submits) and waits for the post-login
 * redirect. Turnstile is off in the e2e server, so there is no widget to wait out.
 */
export async function signIn(
  page: Page,
  user: Pick<SeededUser, 'email' | 'password'>,
  opts: { next?: string } = {},
): Promise<void> {
  await page.goto(opts.next ? `/login?next=${encodeURIComponent(opts.next)}` : '/login')
  await waitForHydration(page)
  await page.locator('#login-email').fill(user.email)
  await page.locator('#login-password').fill(user.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForURL(opts.next ?? '**/dashboard')
}

/**
 * Picks the two earliest enabled days on the poll creator's calendar (`DateOptionsEditor`),
 * paging forward a month at a time if the visible month doesn't have two selectable days yet —
 * which keeps the test correct no matter what day of the month CI happens to run on. Days before
 * today are disabled by the app itself, so every button this finds is already a valid choice.
 */
export async function pickTwoCalendarDays(page: Page): Promise<void> {
  const calendar = page.locator('[data-slot="calendar"]')
  await expect(calendar).toBeVisible()

  const enabledDays = calendar.locator('button[data-day]:not([disabled])')
  for (let guard = 0; guard < 6 && (await enabledDays.count()) < 2; guard++) {
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
  }
  await expect(async () => {
    expect(await enabledDays.count()).toBeGreaterThanOrEqual(2)
  }).toPass({ timeout: 5_000 })

  await enabledDays.nth(0).click()
  await enabledDays.nth(1).click()
}

/**
 * Picks the first enabled day on a booking month picker (`MonthPicker`, built on the same
 * `Calendar` primitive `pickTwoCalendarDays` drives), paging forward a month at a time if the
 * visible month has none yet. `scope` may be a Page or any Locator that contains exactly one
 * calendar — e.g. the reschedule dialog, which has its own picker next to the page's.
 */
export async function pickFirstEnabledDay(scope: Page | Locator): Promise<void> {
  const calendar = scope.locator('[data-slot="calendar"]')
  await expect(calendar).toBeVisible()

  const enabledDays = calendar.locator('button[data-day]:not([disabled])')
  for (let guard = 0; guard < 6 && (await enabledDays.count()) < 1; guard++) {
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
  }
  await expect(async () => {
    expect(await enabledDays.count()).toBeGreaterThanOrEqual(1)
  }).toPass({ timeout: 5_000 })

  await enabledDays.first().click()
}

/**
 * Like `pickFirstEnabledDay`, but never picks "today" — deliberately so a caller that books
 * today's only remaining slot doesn't have to worry about `SlotPicker`'s own documented "no slots
 * left on this day, jump to the next enabled one" fallback
 * (`web/src/components/booking/SlotPicker.tsx`) coincidentally matching an analogous same-time
 * chip on a different day. "Today" is the one day whose slot count can already be partway drained
 * by the time the suite runs (most of a day's 09:00–17:00 Europe/Oslo weekday window may already
 * be in the past by wall-clock afternoon) — any OTHER day is always a full, untouched day, so
 * booking one slot on it can never empty the whole day.
 *
 * Picks the LAST enabled day in the currently visible month rather than the first: in the
 * overwhelmingly common case that's a different day from "today" already, at no extra request
 * cost over `pickFirstEnabledDay` — booking.spec.ts's own two browser contexts each cost two
 * loader-driven fetches just to land on this page, against the public booking rate limiter
 * (internal/bookings/handlers.go), so an unconditional month-forward navigation (an extra fetch
 * pair per context) is a real budget concern, not a style preference. Only pages forward (same
 * fallback loop as `pickFirstEnabledDay`) on the rare day the last enabled day in view IS today —
 * matched via `toLocaleDateString('en-US')` against the `data-day` attribute
 * (`web/src/components/ui/calendar.tsx`), which reads it from a `Date` in the SAME locale
 * (Playwright's own context option, `playwright.config.ts`) and system time zone a plain
 * `new Date()` in this Node process already uses.
 */
export async function pickFirstEnabledDayNotToday(scope: Page | Locator): Promise<void> {
  const calendar = scope.locator('[data-slot="calendar"]')
  await expect(calendar).toBeVisible()

  const todayKey = new Date().toLocaleDateString('en-US')

  const lastNonTodayDay = async () => {
    const enabledDays = calendar.locator('button[data-day]:not([disabled])')
    const count = await enabledDays.count()
    for (let i = count - 1; i >= 0; i--) {
      const day = enabledDays.nth(i)
      if ((await day.getAttribute('data-day')) !== todayKey) return day
    }
    return null
  }

  let target = await lastNonTodayDay()
  for (let guard = 0; guard < 6 && target === null; guard++) {
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
    await expect(async () => {
      const enabledDays = calendar.locator('button[data-day]:not([disabled])')
      expect(await enabledDays.count()).toBeGreaterThanOrEqual(1)
    }).toPass({ timeout: 5_000 })
    target = await lastNonTodayDay()
  }
  if (!target) {
    throw new Error('pickFirstEnabledDayNotToday: no non-today enabled day found within 6 months')
  }
  await target.click()
}
```

- [ ] **Step 4: Remove every `waitForTurnstile` call site**

Apply these exact edits (each `-` line is deleted, each `+` line replaces it):

`e2e/auth.spec.ts`
```diff
-import { expect, signIn, test, waitForHydration, waitForTurnstile } from './fixtures'
+import { expect, signIn, test, waitForHydration } from './fixtures'
```
and delete the two lines `    await waitForTurnstile(page)` (in the sign-up test and the wrong-password test).

`e2e/booking.spec.ts`
```diff
 import {
   expect,
   pickFirstEnabledDayNotToday,
   signIn,
   test,
   waitForHydration,
-  waitForTurnstile,
 } from './fixtures'
```
and delete `    await waitForTurnstile(visitorPage)`.

`e2e/live.spec.ts`
```diff
-import { expect, signIn, test, waitForHydration, waitForTurnstile } from './fixtures'
+import { expect, signIn, test, waitForHydration } from './fixtures'
```
and delete `    await waitForTurnstile(guestPage)`.

`e2e/mobile.spec.ts`
```diff
-import { expect, test, waitForHydration, waitForTurnstile } from './fixtures'
+import { expect, test, waitForHydration } from './fixtures'
```
and delete `    await waitForTurnstile(page)`.

`e2e/poll-flow.spec.ts`
```diff
 import {
   expect,
   pickTwoCalendarDays,
   signIn,
   test,
   waitForHydration,
-  waitForTurnstile,
 } from './fixtures'
```
and delete `    await waitForTurnstile(guestPage)`.

`e2e/signup.spec.ts`
```diff
-import { expect, signIn, test, waitForHydration, waitForTurnstile } from './fixtures'
+import { expect, signIn, test, waitForHydration } from './fixtures'
```
and in `claimSlotAsNewGuest` delete `  await waitForTurnstile(page)`; also update its doc comment line `waits out the Turnstile test widget, and submits` → `and submits`.

`e2e/screenshots.spec.ts`
```diff
 import {
   expect,
   pickFirstEnabledDay,
   pickTwoCalendarDays,
   signIn,
   test,
   waitForHydration,
-  waitForTurnstile,
 } from './fixtures'
```
and delete both `      await waitForTurnstile(guest)` lines (poll page with votes; sign-up sheet).

- [ ] **Step 5: Verify nothing references Turnstile or port 3000 in the harness**

Run: `grep -rn 'waitForTurnstile\|cf-turnstile\|TURNSTILE' e2e playwright.config.ts; grep -rn 'localhost:3000' e2e playwright.config.ts`
Expected: both greps print nothing (exit code 1).

Run: `bunx tsc --noEmit -p . 2>/dev/null || bunx playwright test --list`
Expected: `--list` prints the existing tests (smoke, auth ×3 or ×4, i18n, dashboard, poll-flow, live, mobile, signup, booking, admin ×4) with no TypeScript errors.

- [ ] **Step 6: Run the smoke and auth specs in go mode**

Run: `bunx playwright test e2e/smoke.spec.ts e2e/auth.spec.ts`
Expected: run-server.sh starts `whenweall-e2e-db` + `whenweall-e2e-mailpit`, the server answers on http://localhost:3100/healthz, and all tests pass (`5 passed`). The sign-in tests pass without any widget wait.

- [ ] **Step 7: Commit**

```bash
git add e2e/e2e-env.ts playwright.config.ts e2e/fixtures.ts e2e/auth.spec.ts e2e/booking.spec.ts e2e/live.spec.ts e2e/mobile.spec.ts e2e/poll-flow.spec.ts e2e/signup.spec.ts e2e/screenshots.spec.ts
git commit -m "test(e2e): move the suite to :3100, drop Turnstile, make seed fixtures hard-fail

The app port no longer collides with compose's :3000, so reuseExistingServer
can never silently point the suite at a compose app without the seed route.
TURNSTILE_* is unset for the e2e server (the documented default) and
waitForTurnstile is gone with it. Fixtures narrow the seed result and throw
when pollId/pageId/handle/slug are missing instead of leaving that to per-spec
test.skip guards.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 2: Stronger assertions and no more `test.skip` on seed results

**Files:**
- Modify: `e2e/admin.spec.ts` (whole file)
- Modify: `e2e/dashboard.spec.ts` (whole file)
- Modify: `e2e/auth.spec.ts` (drop the sign-up test; Task 5 replaces it with the full inbox flow)
- Modify: `e2e/booking.spec.ts:16-19`, `e2e/live.spec.ts:8-9`, `e2e/mobile.spec.ts:18-19`, `e2e/signup.spec.ts:30-31`, `e2e/screenshots.spec.ts:60-61,121-123,137-138`

**Interfaces:**
- Consumes: Task 1's typed fixtures (`userWithPoll.pollId` etc. are `string`, never undefined).
- Produces: `e2e/admin.spec.ts` with a `test.describe('admin console', …)` block that Task 13 appends two more tests to.

- [ ] **Step 1: Replace `e2e/admin.spec.ts`**

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'

test.describe('admin console', () => {
  test('a staff user reaches the console and sees the statistics', async ({ page, userStaff }) => {
    await signIn(page, userStaff)
    await page.goto('/admin')
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible()
    await expect(page.getByText('Organizations', { exact: true })).toBeVisible()
    await expect(page.getByText('Mail queue depth')).toBeVisible()
    await expect(page.getByText('Failed jobs')).toBeVisible()
  })

  test('a staff user can find a user by email', async ({ page, userStaff }) => {
    await signIn(page, userStaff)
    await page.goto('/admin/users')
    await waitForHydration(page)

    await page.getByLabel('Search by email or name').fill(userStaff.email)
    await page.getByLabel('Search by email or name').press('Enter')

    await expect(page.getByRole('cell', { name: userStaff.email })).toBeVisible()
  })

  // The SPA renders its generic not-found card rather than a "forbidden" page: there is no reason
  // to confirm to a stranger that an admin area exists here. The API underneath is the real gate
  // and answers 403 (auth.Service.RequireStaff) to a signed-in non-staff caller, 401 to nobody.
  test('an ordinary signed-in user gets a not-found page and a 403 from the admin API', async ({
    page,
    request,
    user,
  }) => {
    await signIn(page, user)
    await page.goto('/admin')
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: "We can't find that page" })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Admin' })).toHaveCount(0)

    const stats = await page.request.get('/api/v1/admin/stats')
    expect(stats.status()).toBe(403)
    const anonymous = await request.get('/api/v1/admin/stats')
    expect(anonymous.status()).toBe(401)
  })

  test('the admin link is hidden from an ordinary user', async ({ page, user }) => {
    await signIn(page, user)
    await page.goto('/dashboard')
    await waitForHydration(page)

    await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0)
  })
})
```

- [ ] **Step 2: Replace `e2e/dashboard.spec.ts`**

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'

test('lists a created poll, duplicates it as a "(copy)", and deletes it', async ({
  page,
  userWithPoll,
}) => {
  await signIn(page, userWithPoll)
  await page.goto('/dashboard')
  await waitForHydration(page)

  // `exact: true` on the title text, not `hasText` on the card: a substring match would also
  // accept "Seeded test poll (copy)", making the original/copy assertions below indistinguishable.
  const cardTitled = (title: string) =>
    page.locator('[data-testid="poll-card"]').filter({ has: page.getByText(title, { exact: true }) })

  const original = cardTitled('Seeded test poll')
  await expect(original).toHaveCount(1)

  await original.getByRole('button', { name: 'Duplicate' }).click()
  // PollCard's duplicate action navigates straight to the new poll.
  await page.waitForURL(/\/p\/[^/?]+$/)
  await expect(page.getByRole('heading', { name: 'Seeded test poll (copy)' })).toBeVisible()

  await page.goto('/dashboard')
  await waitForHydration(page)
  const copy = cardTitled('Seeded test poll (copy)')
  await expect(copy).toHaveCount(1)
  await expect(original).toHaveCount(1)

  await copy.getByRole('button', { name: 'Delete poll' }).click()
  await page.getByRole('button', { name: 'Delete', exact: true }).click()

  await expect(page.getByText('Poll deleted.')).toBeVisible()
  await expect(copy).toHaveCount(0)
  await expect(original).toHaveCount(1)
})
```

- [ ] **Step 3: Replace `e2e/auth.spec.ts`**

The "signing up shows the check-your-inbox screen" test stopped at the heading and never followed the link; Task 5's `auth-email.spec.ts` owns the whole journey now.

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'

test.describe('auth', () => {
  test('signs in with a seeded user and lands on the dashboard', async ({ page, user }) => {
    await signIn(page, user)

    await expect(page).toHaveURL(/\/dashboard$/)
    await expect(page.getByRole('heading', { name: 'Your polls' })).toBeVisible()
  })

  test('shows an error for the wrong password', async ({ page, user }) => {
    await page.goto('/login')
    await waitForHydration(page)

    await page.locator('#login-email').fill(user.email)
    await page.locator('#login-password').fill('definitely-the-wrong-password')
    await page.getByRole('button', { name: 'Sign in' }).click()

    await expect(page.getByText("That email or password isn't right.")).toBeVisible()
    await expect(page).toHaveURL(/\/login/)
  })

  test('signs out and returns to the landing page', async ({ page, user }) => {
    await signIn(page, user)

    await page.getByRole('button', { name: 'Account menu' }).click()
    await page.getByRole('menuitem', { name: 'Sign out' }).click()

    await expect(page).toHaveURL('/')
    await expect(page.getByRole('link', { name: 'Sign in' })).toBeVisible()
    // The session is really gone, not just the header re-rendered.
    expect((await page.request.get('/api/v1/auth/me')).status()).toBe(401)
  })
})
```

- [ ] **Step 4: Delete the `test.skip` guards (the fixtures now guarantee the fields)**

`e2e/booking.spec.ts`
```diff
-  test.skip(!userWithBookingPage.pageId, 'seed route did not return a pageId')
-  const pageId = userWithBookingPage.pageId!
-  const handle = userWithBookingPage.handle!
-  const slug = userWithBookingPage.slug!
+  const { pageId, handle, slug } = userWithBookingPage
```

`e2e/live.spec.ts`
```diff
-  test.skip(!userWithPoll.pollId, 'seed route did not return a pollId')
-  const pollId = userWithPoll.pollId!
+  const { pollId } = userWithPoll
```

`e2e/mobile.spec.ts`
```diff
-    test.skip(!userWithPoll.pollId, 'seed route did not return a pollId')
-    const pollId = userWithPoll.pollId!
+    const { pollId } = userWithPoll
```

`e2e/signup.spec.ts`
```diff
-  test.skip(!userWithSignup.pollId, 'seed route did not return a pollId')
-  const pollId = userWithSignup.pollId!
+  const { pollId } = userWithSignup
```

`e2e/screenshots.spec.ts` — three places:
```diff
-  test.skip(!userWithPoll.pollId, 'seed route did not return a pollId')
-  const pollId = userWithPoll.pollId!
+  const { pollId } = userWithPoll
```
```diff
-  test.skip(!userWithBookingPage.pageId, 'seed route did not return a pageId')
-  const handle = userWithBookingPage.handle!
-  const slug = userWithBookingPage.slug!
+  const { handle, slug } = userWithBookingPage
```
```diff
-  test.skip(!userWithSignup.pollId, 'seed route did not return a pollId')
-  const pollId = userWithSignup.pollId!
+  const { pollId } = userWithSignup
```

- [ ] **Step 5: Verify**

Run: `grep -rn 'test.skip' e2e/`
Expected: nothing (exit 1).

Run: `bunx playwright test e2e/admin.spec.ts e2e/dashboard.spec.ts e2e/auth.spec.ts e2e/live.spec.ts e2e/mobile.spec.ts e2e/signup.spec.ts e2e/booking.spec.ts`
Expected: `12 passed`.

- [ ] **Step 6: Commit**

```bash
git add e2e/admin.spec.ts e2e/dashboard.spec.ts e2e/auth.spec.ts e2e/booking.spec.ts e2e/live.spec.ts e2e/mobile.spec.ts e2e/signup.spec.ts e2e/screenshots.spec.ts
git commit -m "test(e2e): assert the not-found page and API status codes, exact dashboard titles, no seed skips

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 3: run-server.sh with a run marker, pinned images and an image-mode branch; marker-based teardown

**Files:**
- Modify: `e2e/run-server.sh` (whole file)
- Modify: `e2e/global-teardown.ts` (whole file)
- Modify: `.gitignore` (add one line)

**Interfaces:**
- Consumes: `E2E_SERVER`, `APP_PORT`, `RUN_MARKER`, `DB_IMAGE`, `MAILPIT_IMAGE` and the container env from Task 1's `playwright.config.ts` `webServer.env`.
- Produces: marker JSON `{ "mode": "go", "containers": ["whenweall-e2e-db","whenweall-e2e-mailpit"], "dist": true }` at `RUN_MARKER`; image-mode contract used by Task 15: the script exits 1 with an instruction if `http://localhost:$APP_PORT/healthz` is not healthy within 120 s, otherwise blocks (`tail -f /dev/null`) so Playwright can supervise it.

- [ ] **Step 1: Replace `e2e/run-server.sh`**

```bash
#!/usr/bin/env bash
# Started as playwright.config.ts's webServer.command, so Playwright supervises (and, at the end
# of the run, kills) this whole thing as one process tree.
#
# Two modes, selected by E2E_SERVER (default "go"):
#
#   go     Start the throwaway Postgres/Mailpit containers, build the SPA into
#          internal/httpserver/dist, and `exec go run ./cmd/whenweall` on :$APP_PORT. Container
#          startup lives here rather than in Playwright's own `globalSetup` hook because, on this
#          Playwright version, `globalSetup` and `webServer` do not reliably run in the documented
#          order (observed: webServer's DB connection failing with "connection refused" because the
#          container `globalSetup` was supposed to have already started hadn't been created yet).
#
#   image  The built Docker image is already running via `e2e/compose-e2e.sh up -d --wait`
#          (compose.yaml + compose.e2e.yaml). Nothing to start: wait for /healthz, then block so
#          Playwright has a process to supervise. Normally playwright.config.ts's
#          reuseExistingServer short-circuits before this script even runs in image mode; this
#          branch only matters when the stack is NOT up yet, and then it fails fast with the
#          command to run.
#
# Everything this script STARTS is recorded in $RUN_MARKER (a JSON file e2e/global-teardown.ts
# reads and deletes), so teardown only ever removes containers this run created and only restores
# dist/ when this run overwrote it — a developer who keeps `bash e2e/run-server.sh` running and
# lets Playwright reuse it never loses their database to a teardown.
#
# Container names/ports/images/credentials come in as env vars from playwright.config.ts's
# webServer.env, which builds them from e2e/e2e-env.ts — the one place they're actually defined.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

: "${APP_PORT:?APP_PORT must be set (playwright.config.ts passes it from e2e/e2e-env.ts)}"
MODE="${E2E_SERVER:-go}"
MARKER="${RUN_MARKER:-e2e/.run-server-marker.json}"
HEALTHZ="http://localhost:${APP_PORT}/healthz"

if [ "$MODE" = "image" ]; then
  echo "run-server.sh: E2E_SERVER=image — waiting for the compose stack on ${HEALTHZ}" >&2
  for _ in $(seq 1 120); do
    if curl -fsS "$HEALTHZ" >/dev/null 2>&1; then
      # Nothing was started here, so no marker: teardown must leave the compose stack alone
      # (the CI job / developer brings it down with `e2e/compose-e2e.sh down -v`).
      exec tail -f /dev/null
    fi
    sleep 1
  done
  echo "run-server.sh: ${HEALTHZ} not healthy after 120s. Start the image stack first:" >&2
  echo "  docker build -t whenweall:e2e . && e2e/compose-e2e.sh up -d --wait" >&2
  exit 1
fi

: "${DB_CONTAINER:?}" "${DB_IMAGE:?}" "${DB_PORT:?}" "${DB_USER:?}" "${DB_PASSWORD:?}" "${DB_NAME:?}"
: "${MAILPIT_CONTAINER:?}" "${MAILPIT_IMAGE:?}" "${MAILPIT_SMTP_PORT:?}" "${MAILPIT_HTTP_PORT:?}"

docker rm -f "$DB_CONTAINER" >/dev/null 2>&1 || true
docker rm -f "$MAILPIT_CONTAINER" >/dev/null 2>&1 || true

docker run -d --name "$DB_CONTAINER" \
  -e POSTGRES_USER="$DB_USER" -e POSTGRES_PASSWORD="$DB_PASSWORD" -e POSTGRES_DB="$DB_NAME" \
  -p "127.0.0.1:${DB_PORT}:5432" "$DB_IMAGE" >/dev/null

docker run -d --name "$MAILPIT_CONTAINER" \
  -p "127.0.0.1:${MAILPIT_SMTP_PORT}:1025" -p "127.0.0.1:${MAILPIT_HTTP_PORT}:8025" \
  "$MAILPIT_IMAGE" >/dev/null

# Written the moment the containers exist — before anything below can fail — so a broken build or
# a server that never comes up still gets its containers removed by global-teardown.ts.
printf '{"mode":"go","containers":["%s","%s"],"dist":true}\n' "$DB_CONTAINER" "$MAILPIT_CONTAINER" > "$MARKER"

# Poll pg_isready rather than a fixed sleep: a fresh postgres:18-alpine container is usually ready
# in well under a second, but never assume that under load.
ready=0
for _ in $(seq 1 60); do
  if docker exec "$DB_CONTAINER" pg_isready -U "$DB_USER" -d "$DB_NAME" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [ "$ready" -ne 1 ]; then
  echo "run-server.sh: $DB_CONTAINER did not become ready within 30s" >&2
  exit 1
fi

# `go` isn't necessarily on the default PATH a spawned child process inherits.
export PATH="$PATH:$HOME/.local/go/bin:$HOME/go/bin"

(cd web && bun run build)

# The build output lands where internal/httpserver/spa.go's `//go:embed all:dist` expects it —
# global-teardown.ts restores the committed placeholder afterwards (the marker's "dist": true).
rm -rf internal/httpserver/dist/assets
cp -r web/dist/. internal/httpserver/dist/

# exec, not a plain call: replaces this script's process with the Go server, so the SIGTERM
# Playwright sends when tearing down the webServer reaches the actual server directly instead of
# an intermediate shell that might not forward it.
exec go run ./cmd/whenweall
```

- [ ] **Step 2: Replace `e2e/global-teardown.ts`**

```ts
import { execSync } from 'node:child_process'
import { existsSync, readFileSync, rmSync } from 'node:fs'
import { RUN_MARKER } from './e2e-env'

/** What e2e/run-server.sh records about the things IT started for this run. */
type RunMarker = { mode: 'go'; containers: string[]; dist: boolean }

/**
 * Cleans up exactly what `e2e/run-server.sh` recorded in its marker file for this run — the
 * throwaway containers it created, and the `internal/httpserver/dist` placeholder it overwrote
 * with the real build — then deletes the marker.
 *
 * No marker means this run started nothing: either Playwright reused a server a developer is
 * keeping alive (`reuseExistingServer`), or the suite ran in image mode against the compose stack
 * (`E2E_SERVER=image`, brought down by `e2e/compose-e2e.sh down -v`). In both cases tearing down
 * containers by name would destroy something that isn't ours, so this does nothing at all.
 */
export default function globalTeardown(): void {
  if (!existsSync(RUN_MARKER)) return

  let marker: RunMarker
  try {
    marker = JSON.parse(readFileSync(RUN_MARKER, 'utf8')) as RunMarker
  } catch {
    // An unreadable marker is not worth failing a finished run over — drop it and move on.
    rmSync(RUN_MARKER, { force: true })
    return
  }

  for (const name of marker.containers ?? []) {
    try {
      execSync(`docker rm -f ${name}`, { stdio: 'ignore' })
    } catch {
      // Already gone — nothing to clean up.
    }
  }

  if (marker.dist) {
    try {
      execSync('git clean -fdx -- internal/httpserver/dist', { stdio: 'ignore' })
      execSync('git checkout -- internal/httpserver/dist/index.html', { stdio: 'ignore' })
    } catch {
      // Best-effort: a dirty dist/ here is a cosmetic annoyance, never a reason to fail the run.
    }
  }

  rmSync(RUN_MARKER, { force: true })
}
```

- [ ] **Step 3: Ignore the marker**

Append to `.gitignore`, directly under the `blob-report` line:

```
# Written by e2e/run-server.sh for the run that started containers; removed by global-teardown.ts.
e2e/.run-server-marker.json
```

- [ ] **Step 4: Verify the normal path**

Run: `bunx playwright test e2e/smoke.spec.ts && docker ps --format '{{.Names}}' | grep whenweall-e2e; ls e2e/.run-server-marker.json; git status --short internal/httpserver/dist`
Expected: `1 passed`; the `grep` prints nothing (both containers removed); `ls` reports "No such file"; `git status` prints nothing (dist restored).

- [ ] **Step 5: Verify the reuse path is no longer self-destructive**

In one terminal: `set -a; source <(bunx tsx -e "import * as e from './e2e/e2e-env'; for (const [k,v] of Object.entries(e)) if (typeof v !== 'object') console.log(k+'='+JSON.stringify(v))") 2>/dev/null; E2E_SERVER=go APP_PORT=3100 RUN_MARKER=e2e/.run-server-marker.json ENABLE_TEST_ROUTES=true APP_ENV=test APP_URL=http://localhost:3100 PORT=3100 DATABASE_URL='postgres://whenweall:e2e-test-password@localhost:5434/whenweall?sslmode=disable' SMTP_HOST=localhost SMTP_PORT=1026 bash e2e/run-server.sh`
(If `tsx` is unavailable, export `DB_CONTAINER=whenweall-e2e-db DB_IMAGE=postgres:18-alpine DB_PORT=5434 DB_USER=whenweall DB_PASSWORD=e2e-test-password DB_NAME=whenweall MAILPIT_CONTAINER=whenweall-e2e-mailpit MAILPIT_IMAGE=axllent/mailpit:v1.31.0 MAILPIT_SMTP_PORT=1026 MAILPIT_HTTP_PORT=8026 AUTH_SECRET=e2e-not-a-real-secret-YuerLHH9iaTZviHi0y2hkpP` by hand first.)
Then, in a second terminal, delete the marker the manual run wrote (`rm e2e/.run-server-marker.json` — it belongs to the manual run, not to Playwright) and run `bunx playwright test e2e/smoke.spec.ts` twice.
Expected: both runs print `1 passed`, the server in the first terminal keeps running, and `docker ps` still shows both `whenweall-e2e-*` containers after each run. Ctrl-C the first terminal and `docker rm -f whenweall-e2e-db whenweall-e2e-mailpit` to finish.

- [ ] **Step 6: Verify the image branch fails fast when nothing is up**

Run: `E2E_SERVER=image APP_PORT=3100 timeout 130 bash e2e/run-server.sh; echo "exit=$?"`
Expected: after ~120 s, stderr shows `not healthy after 120s. Start the image stack first:` and `exit=1`.

- [ ] **Step 7: Commit**

```bash
git add e2e/run-server.sh e2e/global-teardown.ts .gitignore
git commit -m "test(e2e): marker-based teardown, pinned Mailpit/Postgres images, image-mode branch

run-server.sh records the containers it created (and that it overwrote dist/)
in e2e/.run-server-marker.json; global-teardown.ts removes only what the
marker lists, so a reused developer server survives a test run. Mailpit is
pinned to axllent/mailpit:v1.31.0. E2E_SERVER=image only waits for the
compose stack's /healthz.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 4: Seed route — `failedJob`, forwarded `name`, verified user, explicit sign-in (Go + tests)

**Files:**
- Modify: `internal/httpserver/testroutes.go` (struct fields, `RegisterTestRoutes` signature, `handleSeed`, new helpers)
- Modify: `internal/httpserver/testroutes_test.go` (`seedTestServer`, four new tests)
- Modify: `cmd/whenweall/main.go:165-167` (call site)

**Interfaces:**
- Consumes: `jobs.Dead(ctx, tx, limit) ([]jobs.Job, error)` (internal/jobs/jobs.go); `db.NewID()`; plan A's `(*auth.Service).GetProfile(ctx, userID string) (auth.Profile, error)` with `Profile.Name`; the `users.email_verified_at` column (Limen schema, `migrations/00002_auth.sql`); plan A's signup hook that reads `name` off the `POST /api/v1/auth/signup/credential` body.
- Produces: `POST /api/test/seed` accepts `{ …, "failedJob": true }` and returns `failedJobId: string | null`; every seeded user has `email_verified_at` set and the returned credentials sign in through `/signin/credential`; the dead-lettered row is `kind = 'mail:send'`, `attempts = max_attempts = 5`, `last_error = "e2e: seeded dead-lettered job <id>"` (the id in the text is what `admin.spec.ts` filters the Jobs table on). Signature: `RegisterTestRoutes(mux *http.ServeMux, cfg *config.Config, sqlDB *sql.DB, authSvc *auth.Service, polls SeedPolls, bookings SeedBookings)`.

Note: plan A may already have added an `email_verified_at` update and/or an explicit sign-in to this file so its own e2e gate passes. If so, keep ONE copy of each — the code below is idempotent (`COALESCE`) and the tests below must pass either way.

- [ ] **Step 1: Write the failing tests** — append to `internal/httpserver/testroutes_test.go`

First update `seedTestServer` to pass the DB through (the signature change is what makes the whole file fail to compile until Step 3):

```go
	srv.RegisterAPI(func(mux *http.ServeMux) {
		pollsSvc.Register(mux, authSvc, cfg)
		bookingsSvc.Register(mux, authSvc, cfg)
		httpserver.RegisterTestRoutes(mux, cfg, d, authSvc, pollsSvc, bookingsSvc)
	})
```

`seedTestServer` also needs to hand the tests the DB: change its return list to
`(srv *httpserver.Server, authSvc *auth.Service, pollsSvc *polls.Service, bookingsSvc *bookings.Service, sqlDB *sql.DB)` and `return srv, authSvc, pollsSvc, bookingsSvc, d` at the end; update every existing call site in the file from `srv, a, b, c := seedTestServer(t)` to `srv, a, b, c, _ := seedTestServer(t)` (there are seven). Add `"database/sql"` and `"github.com/refsdal/whenweall/internal/jobs"` to the imports. Then append:

```go
// sessionFor is the test-side twin of testroutes.go's seedTriggerSession: runs a cookie-carrying
// request through authSvc.Middleware and captures the resolved *auth.Session.
func sessionFor(authSvc *auth.Service, cookies []*http.Cookie) *auth.Session {
	var got *auth.Session
	handler := authSvc.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = auth.FromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/probe/session", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

// TestSeed_UserIsVerified pins the one property every Playwright fixture now depends on: plan A
// restored the e-mail verification gate, so a seeded user that is NOT verified would 403
// `email_unverified` on its first real request and every spec would fail at sign-in.
func TestSeed_UserIsVerified(t *testing.T) {
	srv, _, _, _, sqlDB := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{})
	email := stringField(t, seeded, "email")

	var verified bool
	if err := sqlDB.QueryRowContext(context.Background(),
		`SELECT email_verified_at IS NOT NULL FROM users WHERE email = $1`, email,
	).Scan(&verified); err != nil {
		t.Fatalf("query users.email_verified_at: %v", err)
	}
	if !verified {
		t.Fatalf("seeded user %s has no email_verified_at; fixtures would hit the verification gate", email)
	}
}

// TestSeed_ForwardsNameToProfile: the seed body's `name` must become the stored display name
// (plan A's GetProfile), not just an echo in the seed response — vote-signed-in.spec.ts asserts on
// the participant row's name and settings.spec.ts on the header.
func TestSeed_ForwardsNameToProfile(t *testing.T) {
	srv, authSvc, _, _, _ := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{"name": "Seeded Person"})
	cookies := signIn(t, srv, stringField(t, seeded, "email"), stringField(t, seeded, "password"))

	sess := sessionFor(authSvc, cookies)
	if sess == nil {
		t.Fatalf("no session resolved for the seeded user's cookies")
	}
	profile, err := authSvc.GetProfile(context.Background(), sess.UserID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile.Name != "Seeded Person" {
		t.Errorf("profile.Name = %q, want %q", profile.Name, "Seeded Person")
	}
}

func TestSeed_FailedJobInsertsADeadLetteredRow(t *testing.T) {
	srv, _, _, _, sqlDB := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{"failedJob": true})
	jobID := stringField(t, seeded, "failedJobId")

	dead, err := jobs.Dead(context.Background(), sqlDB, 100)
	if err != nil {
		t.Fatalf("jobs.Dead: %v", err)
	}
	for _, j := range dead {
		if j.ID != jobID {
			continue
		}
		if j.Kind != "mail:send" {
			t.Errorf("kind = %q, want mail:send", j.Kind)
		}
		if j.Attempts < j.MaxAttempts {
			t.Errorf("attempts %d < max_attempts %d: row is not dead-lettered", j.Attempts, j.MaxAttempts)
		}
		if j.LastError == nil || !strings.Contains(*j.LastError, jobID) {
			t.Errorf("last_error = %v, want it to contain the job id %s", j.LastError, jobID)
		}
		return
	}
	t.Fatalf("job %s not in jobs.Dead: %+v", jobID, dead)
}

func TestSeed_NoFailedJobByDefault(t *testing.T) {
	srv, _, _, _, _ := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{})
	if seeded["failedJobId"] != nil {
		t.Errorf("failedJobId = %v, want null when failedJob was not requested", seeded["failedJobId"])
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/httpserver/ -run 'TestSeed_' -count=1`
Expected: compile error — `too many arguments in call to httpserver.RegisterTestRoutes` (and `seeded["failedJobId"]`-related failures once it compiles).

- [ ] **Step 3: Implement in `internal/httpserver/testroutes.go`**

Add `"database/sql"` to the imports, and `"github.com/refsdal/whenweall/internal/jobs"` is NOT needed (the insert is plain SQL so this package keeps its dependency graph). Replace the package doc's second bullet ("No manual 'set email_verified' step…") with:

```go
//   - The seeded user IS marked verified (users.email_verified_at) before any session is minted:
//     plan A restored the e-mail verification gate (RequireSession/WithOrgSession 403
//     `email_unverified`), so an unverified seed would be useless to every fixture. Sign-in is
//     explicit (POST /signin/credential) rather than relying on signup's own Set-Cookie, so it
//     keeps working whether or not autoSignInOnSignUp is enabled.
```

Change the request/result structs:

```go
type seedRequest struct {
	Email           string `json:"email"`
	Name            string `json:"name"`
	Password        string `json:"password"`
	WithPoll        bool   `json:"withPoll"`
	WithSignup      bool   `json:"withSignup"`
	WithBookingPage bool   `json:"withBookingPage"`
	// FailedJob inserts one already dead-lettered scheduled_jobs row (attempts == max_attempts) so
	// the admin console's Jobs page has something to list and retry — see seedDeadLetter.
	FailedJob bool   `json:"failedJob"`
	Role      string `json:"role"`
}

type seedResult struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	Name        string  `json:"name"`
	PollID      *string `json:"pollId"`
	PageID      *string `json:"pageId"`
	Handle      *string `json:"handle"`
	Slug        *string `json:"slug"`
	FailedJobID *string `json:"failedJobId"`
}
```

Change the registration and handler signatures and body:

```go
// RegisterTestRoutes mounts POST /api/test/seed — the caller (cmd/whenweall) must only call this
// when cfg.EnableTestRoutes is true (config.Load already hard-fails boot if that's set alongside
// APP_ENV=production, so there is no production code path that can reach here). sqlDB is needed
// for the two things Limen's HTTP surface can't do for us: marking the fresh user verified and
// inserting a dead-lettered job.
func RegisterTestRoutes(mux *http.ServeMux, cfg *config.Config, sqlDB *sql.DB, authSvc *auth.Service, polls SeedPolls, bookings SeedBookings) {
	mux.HandleFunc("POST /api/test/seed", handleSeed(cfg, sqlDB, authSvc, polls, bookings))
}

func handleSeed(cfg *config.Config, sqlDB *sql.DB, authSvc *auth.Service, polls SeedPolls, bookings SeedBookings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !cfg.EnableTestRoutes {
			Err(w, http.StatusNotFound, "not_found", "not found", nil)
			return
		}

		var body seedRequest
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
				Err(w, http.StatusBadRequest, "invalid", "malformed JSON body", nil)
				return
			}
		}
		_ = r.Body.Close()

		email := body.Email
		if email == "" {
			email = fmt.Sprintf("test-%s@example.com", db.NewID())
		}
		name := body.Name
		if name == "" {
			name = "Test User"
		}
		password := body.Password
		if password == "" {
			password = seedDefaultPassword
		}

		if err := seedSignUp(authSvc, email, password, name); err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signup failed: "+err.Error(), nil)
			return
		}

		// Verified BEFORE the first session is minted (a Session is built from the user row as it
		// stands — the a442f9f lesson for the staff role applies to EmailVerified too).
		if err := seedMarkVerified(r.Context(), sqlDB, email); err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: marking verified failed: "+err.Error(), nil)
			return
		}
		if body.Role == "staff" {
			if err := authSvc.MakeStaff(r.Context(), email); err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: MakeStaff failed: "+err.Error(), nil)
				return
			}
		}

		cookies, err := seedSignIn(authSvc, email, password)
		if err != nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: signin failed: "+err.Error(), nil)
			return
		}
		sess := seedTriggerSession(authSvc, cookies)
		if sess == nil {
			Err(w, http.StatusInternalServerError, "internal", "seed: no session established after signin", nil)
			return
		}

		result := seedResult{Email: email, Password: password, Name: name}

		if body.WithPoll {
			id, err := polls.CreateSamplePoll(r.Context(), sess.ActiveOrgID, sess.UserID)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: creating poll failed: "+err.Error(), nil)
				return
			}
			result.PollID = &id
		}
		if body.WithSignup {
			id, err := polls.CreateSampleSignup(r.Context(), sess.ActiveOrgID, sess.UserID)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: creating sign-up sheet failed: "+err.Error(), nil)
				return
			}
			result.PollID = &id
		}
		if body.WithBookingPage {
			pageID, handle, slug, err := bookings.CreateSampleBookingPage(r.Context(), sess.ActiveOrgID, sess.UserID)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: creating booking page failed: "+err.Error(), nil)
				return
			}
			result.PageID = &pageID
			result.Handle = &handle
			result.Slug = &slug
		}
		if body.FailedJob {
			id, err := seedDeadLetter(r.Context(), sqlDB)
			if err != nil {
				Err(w, http.StatusInternalServerError, "internal", "seed: inserting dead-lettered job failed: "+err.Error(), nil)
				return
			}
			result.FailedJobID = &id
		}

		JSON(w, http.StatusOK, result)
	}
}
```

Change `seedSignUp` to forward the name and ignore the signup response cookies, and add the three new helpers (keep `nextSeedRemoteAddr`, `seedTriggerSession` exactly as they are):

```go
// seedSignUp drives Limen's own signup route in-process (authSvc.Handler(), the exact handler
// internal/httpserver.Server mounts at "/api/v1/auth/") via httptest, exactly the way a browser's
// POST to /api/v1/auth/signup/credential would. `name` rides along in the body for plan A's
// after-signup hook, which persists it as the display name (GetProfile). The response's cookies
// are deliberately NOT used: seedSignIn mints the session after the user is marked verified.
func seedSignUp(authSvc *auth.Service, email, password, name string) error {
	payload, err := json.Marshal(map[string]string{"email": email, "password": password, "name": name})
	if err != nil {
		return fmt.Errorf("marshal signup body: %w", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup/credential", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = nextSeedRemoteAddr()
	rec := httptest.NewRecorder()
	authSvc.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("signup/credential: status %d: %s", res.StatusCode, respBody)
	}
	return nil
}

// seedSignIn is seedSignUp's counterpart for POST /signin/credential — the same route every
// Playwright `signIn` helper submits to — returning the session cookies Limen sets.
func seedSignIn(authSvc *auth.Service, email, password string) ([]*http.Cookie, error) {
	payload, err := json.Marshal(map[string]string{"credential": email, "password": password})
	if err != nil {
		return nil, fmt.Errorf("marshal signin body: %w", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signin/credential", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = nextSeedRemoteAddr()
	rec := httptest.NewRecorder()
	authSvc.Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("signin/credential: status %d: %s", res.StatusCode, respBody)
	}
	return res.Cookies(), nil
}

// seedMarkVerified sets users.email_verified_at for a freshly signed-up user. COALESCE keeps an
// already-set timestamp (e.g. if a hook marked the user verified), so this is safe to run twice.
func seedMarkVerified(ctx context.Context, sqlDB *sql.DB, email string) error {
	res, err := sqlDB.ExecContext(ctx,
		`UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE email = $1`, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("expected to update 1 user row for %s, updated %d", email, n)
	}
	return nil
}

// seedDeadLetterKind is a real mail kind so the row reads like a genuine dead letter on the admin
// console's Jobs page. If the console retries it, the mailer handler fails to decode the payload
// and the worker walks it back to dead-lettered after its retry budget — harmless noise, and
// exactly what a retry of a broken mail job does in production.
const seedDeadLetterKind = "mail:send"

// seedDeadLetter inserts one scheduled_jobs row with its attempt budget already spent (the
// dead-letter condition internal/jobs.Dead selects on: attempts >= max_attempts). The job id is
// embedded in last_error so an e2e spec can find exactly its own row in a shared table.
func seedDeadLetter(ctx context.Context, sqlDB *sql.DB) (string, error) {
	id := db.NewID()
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO scheduled_jobs (id, kind, room_key, run_at, payload, attempts, max_attempts, last_error)
		VALUES ($1, $2, NULL, now() - interval '1 hour', '{"e2e": true}'::jsonb, 5, 5, $3)
	`, id, seedDeadLetterKind, "e2e: seeded dead-lettered job "+id)
	if err != nil {
		return "", err
	}
	return id, nil
}
```

Update the call site in `cmd/whenweall/main.go`:

```go
		if cfg.EnableTestRoutes {
			httpserver.RegisterTestRoutes(mux, cfg, sqlDB, authSvc, pollsSvc, bookingsSvc)
		}
```

- [ ] **Step 4: Run the tests**

Run: `go build ./... && go vet ./internal/httpserver/ ./cmd/... && go test ./internal/httpserver/ -run 'TestSeed_' -count=1 -v 2>&1 | tail -30`
Expected: every `TestSeed_*` test prints `--- PASS`, including the four new ones and the pre-existing `TestSeed_DefaultsToAVerifiedSignInableUser`, `TestSeed_ManySeedsAgainstOneServerNeverRateLimit`, `TestSeed_StaffRoleForwarded`.

Run: `golangci-lint run ./internal/httpserver/... ./cmd/...`
Expected: no findings.

- [ ] **Step 5: Confirm the Playwright fixtures still seed**

Run: `bunx playwright test e2e/auth.spec.ts e2e/admin.spec.ts`
Expected: `7 passed` (the seeded users pass the verification gate and staff seeding still works).

- [ ] **Step 6: Commit**

```bash
git add internal/httpserver/testroutes.go internal/httpserver/testroutes_test.go cmd/whenweall/main.go
git commit -m "feat(testroutes): seed verified users with a display name, optional dead-lettered job

The seed route marks users verified before minting a session (plan A's gate),
signs in explicitly instead of relying on signup cookies, forwards name to the
signup body, and inserts a dead-lettered mail:send row when failedJob is set so
the admin Jobs page has something to retry.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 5: `e2e/mailpit.ts` helper and `auth-email.spec.ts` (verification, unverified card + resend, password reset)

**Files:**
- Create: `e2e/mailpit.ts`
- Create: `e2e/auth-email.spec.ts`

**Interfaces:**
- Consumes: `APP_URL`, `MAILPIT_HTTP_PORT` (Task 1); plan A's UI — `/verify-email?token=` consumes the token and renders the done card (heading `Email verified`); `/login` after a sign-in attempt by an unverified user shows the text `Your email isn't verified yet.` with a `Resend verification email` button whose success text starts with `Verification email sent`; `/forgot-password` and `/reset-password?token=` work without Turnstile. Mail subjects from `web/messages/en.json`: `Verify your email address`, `Reset your password`.
- Produces (used by Tasks 10, 11, 14): from `e2e/mailpit.ts` — `type MailpitMessage`, `searchMail(request, to, subject?)`, `countMail(request, to, subject?)`, `waitForMail(request, to, { subject?, minCount?, timeout? })`, `readMail(request, id)`, `extractLinks(message, pathPrefix?)`, `extractLink(message, pathPrefix)`.

- [ ] **Step 1: Create `e2e/mailpit.ts`**

```ts
import { expect, type APIRequestContext } from '@playwright/test'
import { APP_URL, MAILPIT_HTTP_PORT } from './e2e-env'

/**
 * Reads the Mailpit inbox the e2e server delivers into (SMTP :1026 → HTTP API :8026, both modes).
 *
 * Endpoints (Mailpit v1 API, pinned image in e2e-env.ts):
 *   GET  /api/v1/search?query=<q>&limit=<n>  → { messages: [MailpitSummary, …] } newest first
 *   GET  /api/v1/message/{ID}                → MailpitMessage (decoded Text + HTML bodies)
 * The search syntax `to:"addr@example.com"` scopes to one recipient. Both shapes were checked
 * against Mailpit's swagger.json while planning; if a future bump changes them, this file is the
 * only place to fix.
 *
 * Every mail leaves the Go server through the scheduled_jobs worker (5s poll, NOTIFY-woken), so
 * "the mail exists" is always an eventually-true assertion: use `waitForMail`/`expect.poll`, never
 * a one-shot `searchMail` right after the action that triggers it.
 */
export const MAILPIT_API = `http://localhost:${MAILPIT_HTTP_PORT}/api/v1`

export type MailpitAddress = { Name: string; Address: string }

export type MailpitSummary = {
  ID: string
  MessageID: string
  From: MailpitAddress
  To: MailpitAddress[]
  Subject: string
  Snippet: string
  Created: string
}

export type MailpitMessage = MailpitSummary & { Text: string; HTML: string }

/** Every message addressed to `to`, newest first, optionally narrowed by subject. */
export async function searchMail(
  request: APIRequestContext,
  to: string,
  subject?: RegExp,
): Promise<MailpitSummary[]> {
  const response = await request.get(`${MAILPIT_API}/search`, {
    params: { query: `to:"${to}"`, limit: 50 },
  })
  if (!response.ok()) {
    throw new Error(
      `Mailpit search responded ${response.status()} — is the Mailpit container up on :${MAILPIT_HTTP_PORT}?`,
    )
  }
  const { messages } = (await response.json()) as { messages: MailpitSummary[] }
  return subject ? messages.filter((message) => subject.test(message.Subject)) : messages
}

export async function countMail(
  request: APIRequestContext,
  to: string,
  subject?: RegExp,
): Promise<number> {
  return (await searchMail(request, to, subject)).length
}

export async function readMail(request: APIRequestContext, id: string): Promise<MailpitMessage> {
  const response = await request.get(`${MAILPIT_API}/message/${id}`)
  if (!response.ok()) {
    throw new Error(`Mailpit message ${id} responded ${response.status()}`)
  }
  return (await response.json()) as MailpitMessage
}

/**
 * Polls until at least `minCount` (default 1) messages to `to` (matching `subject`, if given) exist,
 * then returns the newest one in full.
 */
export async function waitForMail(
  request: APIRequestContext,
  to: string,
  opts: { subject?: RegExp; minCount?: number; timeout?: number } = {},
): Promise<MailpitMessage> {
  const minCount = opts.minCount ?? 1
  let matches: MailpitSummary[] = []
  await expect
    .poll(
      async () => {
        matches = await searchMail(request, to, opts.subject)
        return matches.length
      },
      {
        timeout: opts.timeout ?? 30_000,
        message: `waiting for ${minCount} mail(s) to ${to}${opts.subject ? ` with subject ${opts.subject}` : ''}`,
      },
    )
    .toBeGreaterThanOrEqual(minCount)
  return readMail(request, matches[0]!.ID)
}

/**
 * Every link into the app (APP_URL-prefixed) found in the message's text and HTML bodies,
 * de-duplicated, optionally narrowed to a path prefix such as `/verify-email`.
 */
export function extractLinks(
  message: Pick<MailpitMessage, 'Text' | 'HTML'>,
  pathPrefix = '/',
): URL[] {
  const escaped = APP_URL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const pattern = new RegExp(`${escaped}[^\\s"'<>)\\]]*`, 'g')
  const found = new Set<string>()
  for (const body of [message.Text ?? '', message.HTML ?? '']) {
    for (const match of body.matchAll(pattern)) {
      // HTML bodies escape `&` in query strings; the text body does not.
      found.add(match[0].replace(/&amp;/g, '&'))
    }
  }
  return [...found].map((href) => new URL(href)).filter((url) => url.pathname.startsWith(pathPrefix))
}

/** The first app link under `pathPrefix`, or a loud failure naming the mail it was missing from. */
export function extractLink(message: MailpitMessage, pathPrefix: string): URL {
  const [link] = extractLinks(message, pathPrefix)
  if (!link) {
    throw new Error(`no ${APP_URL}${pathPrefix}… link in mail "${message.Subject}":\n${message.Text}`)
  }
  return link
}
```

- [ ] **Step 2: Create `e2e/auth-email.spec.ts`**

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'
import { countMail, extractLink, waitForMail } from './mailpit'

/**
 * The three journeys that only exist through the inbox. Plan A restored the verification gate:
 * an unverified account can sign in only far enough to be told to verify, and the resend button
 * on that card re-delivers the mail.
 */
test.describe('e-mail flows', () => {
  test('sign up, get told to verify, resend, follow the link, then sign in for real', async ({
    page,
    browser,
    request,
  }) => {
    const email = `verify-${Date.now()}@example.com`
    const password = 'correct horse battery staple'

    await page.goto('/signup')
    await waitForHydration(page)
    await page.locator('#signup-name').fill('Verified Person')
    await page.locator('#signup-email').fill(email)
    await page.locator('#signup-password').fill(password)
    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page.getByRole('heading', { name: 'Check your inbox' })).toBeVisible({
      timeout: 15_000,
    })

    const verifyMail = await waitForMail(request, email, { subject: /Verify your email address/ })
    expect(verifyMail.Text).toContain('Verified Person')

    // --- signing in before verifying: the unverified card, not the dashboard ---
    await page.goto('/login')
    await waitForHydration(page)
    await page.locator('#login-email').fill(email)
    await page.locator('#login-password').fill(password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText("Your email isn't verified yet.")).toBeVisible()
    await expect(page).not.toHaveURL(/\/dashboard/)

    // --- resend delivers a second copy ---
    await page.getByRole('button', { name: 'Resend verification email' }).click()
    await expect(page.getByText(/Verification email sent/)).toBeVisible()
    await expect
      .poll(() => countMail(request, email, /Verify your email address/), { timeout: 30_000 })
      .toBe(2)

    // --- the emailed link verifies the address ---
    const link = extractLink(verifyMail, '/verify-email')
    expect(link.searchParams.get('token')).toBeTruthy()
    await page.goto(link.pathname + link.search)
    await waitForHydration(page)
    await expect(page.getByRole('heading', { name: 'Email verified' })).toBeVisible()

    // --- a clean context proves the credentials now sign in all the way to the dashboard ---
    const fresh = await browser.newContext()
    const freshPage = await fresh.newPage()
    try {
      await signIn(freshPage, { email, password })
      await expect(freshPage.getByRole('heading', { name: 'Your polls' })).toBeVisible()
      const me = (await (await freshPage.request.get('/api/v1/auth/me')).json()) as {
        user: { emailVerified: boolean; name: string }
      }
      expect(me.user.emailVerified).toBe(true)
      expect(me.user.name).toBe('Verified Person')
    } finally {
      await fresh.close()
    }
  })

  test('forgot password: the emailed link sets a new password that signs in', async ({
    page,
    request,
    user,
  }) => {
    await page.goto('/forgot-password')
    await waitForHydration(page)
    await page.locator('#forgot-email').fill(user.email)
    await page.getByRole('button', { name: 'Send reset link' }).click()
    await expect(page.getByRole('heading', { name: 'Check your inbox' })).toBeVisible()

    const resetMail = await waitForMail(request, user.email, { subject: /Reset your password/ })
    const link = extractLink(resetMail, '/reset-password')
    expect(link.searchParams.get('token')).toBeTruthy()

    const newPassword = `Fresh-${Date.now()}-passphrase`
    await page.goto(link.pathname + link.search)
    await waitForHydration(page)
    await page.locator('#reset-password').fill(newPassword)
    await page.getByRole('button', { name: 'Reset password' }).click()
    await expect(page.getByRole('heading', { name: 'Password updated' })).toBeVisible()

    // The old password is dead …
    await page.goto('/login')
    await waitForHydration(page)
    await page.locator('#login-email').fill(user.email)
    await page.locator('#login-password').fill(user.password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText("That email or password isn't right.")).toBeVisible()

    // … and the new one works.
    await signIn(page, { email: user.email, password: newPassword })
    await expect(page.getByRole('heading', { name: 'Your polls' })).toBeVisible()
  })
})
```

- [ ] **Step 3: Run the spec**

Run: `bunx playwright test e2e/auth-email.spec.ts`
Expected: `2 passed`. If the first `waitForMail` times out, open `http://localhost:8026` while the server is up (`bash e2e/run-server.sh` in a terminal, see Task 3 step 5) and check the search box accepts `to:"<addr>"`; if Mailpit's search syntax differs, adjust `searchMail`'s `query` in one place.

- [ ] **Step 4: Verify the Mailpit API shape at run time (once)**

With the go-mode server running in a terminal (Task 3 step 5), run:
`curl -s 'http://localhost:8026/api/v1/search?query=to:%22nobody@example.com%22&limit=1' | head -c 300; echo; curl -s http://localhost:8026/api/v1/messages?limit=1 | head -c 300`
Expected: JSON containing `"messages":[` (empty array is fine) — confirming the `/api/v1/search` path and `messages` key the helper relies on.

- [ ] **Step 5: Commit**

```bash
git add e2e/mailpit.ts e2e/auth-email.spec.ts
git commit -m "test(e2e): read Mailpit to cover verification, resend and password reset end to end

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 6: `comments.spec.ts` — guest posts, owner sees it live, owner deletes, guest tab drops it

**Files:**
- Create: `e2e/comments.spec.ts`

**Interfaces:**
- Consumes: `userWithPoll` (seeded poll has `allowComments: true`, internal/polls/seed.go); `Comments.tsx` — the `<section aria-labelledby="poll-comments-heading">` (role `region`, name starts with `Comments`), guest name input `aria-label="Your name"`, textarea `aria-label="Comments"`, button `Post comment`, toast `Comment posted.`, owner delete button `Delete comment from {name}`, toast `Comment deleted.`; comments emit `poll.changed {entity: "comment"}` room events (internal/polls/participants.go), so both tabs update without a reload.
- Produces: nothing consumed later.

- [ ] **Step 1: Create `e2e/comments.spec.ts`**

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'

test('a guest comment appears live for the owner, and the owner deleting it drops it live for the guest', async ({
  page,
  browser,
  userWithPoll,
}) => {
  const pollPath = `/p/${userWithPoll.pollId}`

  // Context A: the owner, watching the poll.
  await signIn(page, userWithPoll)
  await page.goto(pollPath)
  await waitForHydration(page)
  const ownerComments = page.getByRole('region', { name: /^Comments/ })
  await expect(ownerComments).toBeVisible()
  await expect(ownerComments.getByText('No comments yet. Start the conversation.')).toBeVisible()

  // Context B: an anonymous guest.
  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(pollPath)
    await waitForHydration(guestPage)
    const guestComments = guestPage.getByRole('region', { name: /^Comments/ })
    await expect(guestComments).toBeVisible()

    const body = `Late start works for me ${Date.now()}`
    // Scoped to the region: the vote grid's "add yourself" row has a "Your name" input of its own.
    await guestComments.getByRole('textbox', { name: 'Your name' }).fill('Comment Guest')
    await guestComments.getByRole('textbox', { name: /^Comments/ }).fill(body)
    await guestComments.getByRole('button', { name: 'Post comment' }).click()

    await expect(guestPage.getByText('Comment posted.')).toBeVisible()
    await expect(guestComments.getByText(body)).toBeVisible()
    // A guest is not the owner and has no session, so no delete control on their own comment.
    await expect(
      guestComments.getByRole('button', { name: 'Delete comment from Comment Guest' }),
    ).toHaveCount(0)

    // --- the owner's tab picks it up live, without a reload ---
    const ownerCopy = ownerComments.getByText(body)
    await expect(ownerCopy).toBeVisible({ timeout: 10_000 })

    // --- the owner deletes it ---
    // The delete button sits at opacity 0 until the row is hovered (sm:opacity-0) — still "visible"
    // to Playwright, but hovering first mirrors what a person does and avoids a flaky click.
    await ownerCopy.hover()
    await ownerComments.getByRole('button', { name: 'Delete comment from Comment Guest' }).click()
    await expect(page.getByText('Comment deleted.')).toBeVisible()
    await expect(ownerCopy).toHaveCount(0)

    // --- and the guest's tab drops it live too ---
    await expect(guestComments.getByText(body)).toHaveCount(0, { timeout: 10_000 })
    await expect(guestComments.getByText('No comments yet. Start the conversation.')).toBeVisible()
  } finally {
    await guestContext.close()
  }
})
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/comments.spec.ts`
Expected: `1 passed`.

- [ ] **Step 3: Commit**

```bash
git add e2e/comments.spec.ts
git commit -m "test(e2e): cover posting and deleting a comment across two live contexts

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 7: `vote-signed-in.spec.ts` — a signed-in user votes; a second signed-in context edits the same row

**Files:**
- Create: `e2e/vote-signed-in.spec.ts`

**Interfaces:**
- Consumes: `user` (the voter; Task 4 makes `user.name === 'E2E User'` the stored display name) and `userWithPoll` (the poll); `AddYourselfRow` (`data-testid="add-yourself-row"`, name input `aria-label="Your name"` prefilled with the session name, `button[data-answer]` cells), `ParticipantRow` (`data-testid="participant-row-<id>"`, `You` badge, `Edit {name}'s answer` button), buttons `Save my answer` / `Update answer`.
- Produces: nothing consumed later.

- [ ] **Step 1: Create `e2e/vote-signed-in.spec.ts`**

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'

/**
 * Every other voter in the suite is an anonymous context holding an HMAC edit token. A signed-in
 * voter takes the other persistence path — the row is bound to the user id, so a SECOND device
 * (context) signed in as the same person must see it as "You" and be allowed to edit it, with no
 * token to hand over.
 */
test('a signed-in user votes, and a second signed-in context edits that same answer', async ({
  page,
  browser,
  user,
  userWithPoll,
}) => {
  const pollPath = `/p/${userWithPoll.pollId}`

  // Device 1: vote.
  await signIn(page, user)
  await page.goto(pollPath)
  await waitForHydration(page)
  await expect(page.getByTestId('vote-grid')).toBeVisible()

  const addRow = page.getByTestId('add-yourself-row')
  // The name is prefilled from the session (use-answer-draft.ts) — proves the profile name is used.
  await expect(addRow.getByRole('textbox', { name: 'Your name' })).toHaveValue(user.name)
  const cells = addRow.locator('button[data-answer]')
  await expect(cells).toHaveCount(2)
  await cells.nth(0).click() // yes
  await cells.nth(1).click() // yes
  await page.getByRole('button', { name: 'Save my answer' }).click()

  const rows = page.locator('[data-testid^="participant-row-"]')
  const myRow = rows.filter({ hasText: 'You' })
  await expect(myRow).toHaveCount(1)
  await expect(myRow).toContainText(user.name)
  await expect(myRow.locator('span[data-answer="yes"]')).toHaveCount(2)
  // Having answered, the add-yourself row is gone — a signed-in user gets exactly one row.
  await expect(addRow).toHaveCount(0)

  // Device 2: same person, fresh context, no edit token anywhere.
  const second = await browser.newContext()
  const secondPage = await second.newPage()
  try {
    await signIn(secondPage, user)
    await secondPage.goto(pollPath)
    await waitForHydration(secondPage)

    const theirRow = secondPage.locator('[data-testid^="participant-row-"]').filter({ hasText: 'You' })
    await expect(theirRow).toHaveCount(1)
    await theirRow.getByRole('button', { name: `Edit ${user.name}'s answer` }).click()

    const editCells = secondPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await expect(editCells).toHaveCount(2)
    await editCells.nth(0).click() // yes -> if need be
    await secondPage.getByRole('button', { name: 'Update answer' }).click()

    await expect(theirRow.locator('span[data-answer="ifneedbe"]')).toHaveCount(1)
    await expect(theirRow.locator('span[data-answer="yes"]')).toHaveCount(1)
  } finally {
    await second.close()
  }

  // Device 1 sees the edit live — same row, no duplicate.
  await expect(myRow.locator('span[data-answer="ifneedbe"]')).toHaveCount(1, { timeout: 10_000 })
  await expect(rows).toHaveCount(1)
})
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/vote-signed-in.spec.ts`
Expected: `1 passed`.

- [ ] **Step 3: Commit**

```bash
git add e2e/vote-signed-in.spec.ts
git commit -m "test(e2e): cover the session-bound vote path from two signed-in contexts

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 8: `creator.spec.ts` — a text poll voted by a guest; a dates poll with one time slot applied to all days and a time-zone switch

**Files:**
- Create: `e2e/creator.spec.ts`

**Interfaces:**
- Consumes: creator wizard (`TypeStep` type cards are `<button aria-pressed>` whose accessible name starts with the card title `Anything else`; `TextOptionsEditor` inputs `aria-label="Option {n}"`, button `Add option`; `TimeSlotEditor` quick-start chips (`button` named `10:00`), button `Add time`, chip text `10:00 – 11:00`, button `Apply to all days`, toast `Times copied to every selected day.`; `OptionsStep` badge `N options`); poll page `OptionHeader` (`data-testid="option-header-<id>"`, `columnheader` role) and `TimezoneSwitch` (`change` popover trigger, `<select id="poll-timezone">`, text `Times shown in {zone}`).
- Produces: nothing consumed later.

- [ ] **Step 1: Create `e2e/creator.spec.ts`**

```ts
import { expect, pickTwoCalendarDays, signIn, test, waitForHydration } from './fixtures'

// The creator defaults its time zone to the browser's; pin it so the assertions on rendered clock
// times below are the same on every machine (and so the Asia/Tokyo switch has a known offset).
test.use({ timezoneId: 'Europe/Oslo' })

async function openWizard(page: import('@playwright/test').Page, title: string): Promise<void> {
  await page.goto('/new')
  await waitForHydration(page)
  await expect(page.getByTestId('creator-wizard')).toBeVisible()
  await page.locator('#creator-title').fill(title)
}

async function createAndDismissShare(page: import('@playwright/test').Page): Promise<string> {
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Create poll' }).click()
  await page.waitForURL(/\/p\/[^/?]+/)
  const shareDialog = page.getByRole('dialog', { name: 'Share this poll' })
  await expect(shareDialog).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(shareDialog).toBeHidden()
  const url = new URL(page.url())
  return `${url.origin}${url.pathname}`
}

test('a choice poll ("Pizza / Sushi / Thai") is created through the wizard and a guest votes on it', async ({
  page,
  browser,
  user,
}) => {
  await signIn(page, user)
  await openWizard(page, `Dinner ${Date.now()}`)

  // Type card: accessible name is "Anything else" + its description, hence the anchored regex.
  await page.getByRole('button', { name: /^Anything else/ }).click()
  await expect(page.getByRole('button', { name: /^Anything else/ })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await page.getByRole('textbox', { name: 'Option 1' }).fill('Pizza')
  await page.getByRole('textbox', { name: 'Option 2' }).fill('Sushi')
  await page.getByRole('button', { name: 'Add option' }).click()
  await page.getByRole('textbox', { name: 'Option 3' }).fill('Thai')
  await expect(page.getByText('3 options')).toBeVisible()

  const pollUrl = await createAndDismissShare(page)

  for (const label of ['Pizza', 'Sushi', 'Thai']) {
    await expect(page.getByRole('columnheader', { name: label })).toBeVisible()
  }
  // A choice poll has no clock times, so no time-zone switch is offered.
  await expect(page.getByText(/^Times shown in /)).toHaveCount(0)

  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(pollUrl)
    await waitForHydration(guestPage)
    await expect(guestPage.getByTestId('vote-grid')).toBeVisible()

    const guestName = `Hungry Guest ${Date.now()}`
    await guestPage.getByTestId('add-yourself-row').getByLabel('Your name').fill(guestName)
    const cells = guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await expect(cells).toHaveCount(3)
    await cells.nth(0).click() // Pizza: yes
    await cells.nth(2).click() // Thai: yes
    await cells.nth(2).click() // Thai: if need be
    await guestPage.getByRole('button', { name: 'Save my answer' }).click()

    const guestRow = guestPage.locator('[data-testid^="participant-row-"]').filter({ hasText: 'You' })
    await expect(guestRow).toContainText(guestName)
    await expect(guestRow.locator('span[data-answer="yes"]')).toHaveCount(1)
    await expect(guestRow.locator('span[data-answer="ifneedbe"]')).toHaveCount(1)

    // The owner's tab gets the row live.
    await expect(
      page.locator('[data-testid^="participant-row-"]').filter({ hasText: guestName }),
    ).toBeVisible({ timeout: 10_000 })
  } finally {
    await guestContext.close()
  }
})

test('a dates poll with one time slot applied to all days shows clock times, and the time-zone switch re-renders them', async ({
  page,
  user,
}) => {
  await signIn(page, user)
  await openWizard(page, `Stand-up ${Date.now()}`)
  // "Dates & times" is the default type — straight on.
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await pickTwoCalendarDays(page)
  await expect(page.getByText('2 options')).toBeVisible()

  // The selected-days list: each <li> carries a "Remove {day}" button, so the first such <li> is
  // the first day's card (its nested slot list has no such button yet).
  const firstDay = page
    .locator('li')
    .filter({ has: page.getByRole('button', { name: /^Remove / }) })
    .first()
  await firstDay.getByRole('button', { name: '10:00', exact: true }).click()
  // Default duration is 1 h, so the preview and the chip read 10:00 – 11:00.
  await firstDay.getByRole('button', { name: 'Add time' }).click()
  await expect(firstDay.getByText('10:00 – 11:00')).toBeVisible()

  await firstDay.getByRole('button', { name: 'Apply to all days' }).click()
  await expect(page.getByText('Times copied to every selected day.')).toBeVisible()
  // A day with a slot stops being an all-day option: one slot per day, two options in total.
  await expect(page.getByText('10:00 – 11:00')).toHaveCount(2)
  await expect(page.getByText('2 options')).toBeVisible()

  await createAndDismissShare(page)

  const headers = page.getByTestId(/^option-header-/)
  await expect(headers).toHaveCount(2)
  await expect(headers.first()).toContainText('10:00')
  await expect(headers.first()).toContainText('– 11:00')
  await expect(page.getByText('Times shown in Europe/Oslo')).toBeVisible()

  // Switch the viewer's zone: Oslo is UTC+1 (winter) or UTC+2 (summer), Tokyo UTC+9, so 10:00
  // Oslo renders as 18:00 or 17:00 in Tokyo depending on the date the suite runs.
  await page.getByRole('button', { name: 'change' }).click()
  await page.locator('#poll-timezone').selectOption('Asia/Tokyo')
  await expect(page.getByText('Times shown in Asia/Tokyo')).toBeVisible()
  await expect(headers.first()).not.toContainText('10:00')
  await expect(headers.first()).toContainText(/1[78]:00/)
  await expect(headers.first()).toContainText(/– 1[89]:00/)

  // Back to the organiser's zone via the reset link in the same popover.
  await page.getByRole('button', { name: "Use the organiser's zone (Europe/Oslo)" }).click()
  await expect(headers.first()).toContainText('10:00')
})
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/creator.spec.ts`
Expected: `2 passed`.

- [ ] **Step 3: Commit**

```bash
git add e2e/creator.spec.ts
git commit -m "test(e2e): drive the wizard for a choice poll and a time-slot dates poll with a zone switch

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 9: `poll-edit.spec.ts` — remove a voted option (confirmation), add one, save; a past deadline closes the poll for a guest

**Files:**
- Create: `e2e/poll-edit.spec.ts`

**Interfaces:**
- Consumes: `/p/{id}/edit` (`data-testid="poll-editor"`, `Save changes`, confirmation dialog `Remove options with votes?` with body `1 vote will be deleted along with the removed option.` and button `Save anyway`, toast `Poll updated.`); `DateOptionsEditor` day cards with `Remove {day}` buttons and the calendar (`[data-slot="calendar"]`, `Go to the Next Month`); `SettingsStep` deadline switch (`role=switch`, label `Voting deadline`) and `<input type="date" id="creator-deadline-date">`; `PollPage` status badge `Closed` and notice `Voting is closed. The answers are still here to read.`; the `poll.deadline` job (internal/polls/service.go, `timers.go`) closes a poll whose `deadline_at` is already past on the worker's next claim (≤ 5 s; `NOTIFY`-woken).
- Produces: nothing consumed later.

- [ ] **Step 1: Create `e2e/poll-edit.spec.ts`**

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'

test('the owner edits a voted poll (confirming the lost vote), adds a day, then a past deadline closes it for a guest', async ({
  page,
  browser,
  userWithPoll,
}) => {
  const { pollId } = userWithPoll
  const pollPath = `/p/${pollId}`

  // --- a guest votes yes on both seeded options ---
  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(pollPath)
    await waitForHydration(guestPage)
    await guestPage.getByTestId('add-yourself-row').getByLabel('Your name').fill('Edit Guest')
    const cells = guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await expect(cells).toHaveCount(2)
    await cells.nth(0).click()
    await cells.nth(1).click()
    await guestPage.getByRole('button', { name: 'Save my answer' }).click()
    const guestRow = guestPage.locator('[data-testid^="participant-row-"]').filter({ hasText: 'You' })
    await expect(guestRow.locator('span[data-answer="yes"]')).toHaveCount(2)

    // --- the owner removes the first day and adds another ---
    await signIn(page, userWithPoll)
    await page.goto(`${pollPath}/edit`)
    await waitForHydration(page)
    await expect(page.getByTestId('poll-editor')).toBeVisible()
    await expect(page.getByText('2 options')).toBeVisible()

    await page.getByRole('button', { name: /^Remove / }).first().click()
    await expect(page.getByText('1 option', { exact: true })).toBeVisible()

    // A day two months out can never coincide with the seeded options (2–3 days ahead), so clicking
    // it always ADDS a day rather than toggling one off.
    const calendar = page.locator('[data-slot="calendar"]')
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
    await calendar.locator('button[data-day]:not([disabled])').first().click()
    await expect(page.getByText('2 options')).toBeVisible()

    await page.getByRole('button', { name: 'Save changes' }).click()
    const warning = page.getByRole('dialog', { name: 'Remove options with votes?' })
    await expect(warning).toBeVisible()
    await expect(warning).toContainText('1 vote will be deleted along with the removed option.')
    await warning.getByRole('button', { name: 'Save anyway' }).click()

    await page.waitForURL(`**${pollPath}`)
    await expect(page.getByText('Poll updated.')).toBeVisible()
    await expect(page.getByTestId(/^option-header-/)).toHaveCount(2)

    // The guest's row lost exactly the vote on the removed day.
    await guestPage.reload()
    await waitForHydration(guestPage)
    await expect(guestRow.locator('span[data-answer="yes"]')).toHaveCount(1)
    await expect(guestPage.getByTestId(/^option-header-/)).toHaveCount(2)

    // --- a deadline that has already passed closes the poll ---
    await page.goto(`${pollPath}/edit`)
    await waitForHydration(page)
    await page.getByRole('switch', { name: 'Voting deadline' }).click()
    const yesterday = new Date(Date.now() - 86_400_000).toISOString().slice(0, 10)
    await page.locator('#creator-deadline-date').fill(yesterday)
    await page.getByRole('button', { name: 'Save changes' }).click()
    await page.waitForURL(`**${pollPath}`)
    await expect(page.getByText('Poll updated.')).toBeVisible()

    // The deadline job fires on the worker's next claim and the room event re-renders the owner's
    // page: status badge "Closed" (the countdown pill also reads "Closed" once expired).
    await expect(page.getByText('Closed', { exact: true }).first()).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('button', { name: 'Reopen voting' })).toBeVisible()

    // The guest sees the closed state and can no longer edit their answer.
    await expect(
      guestPage.getByText('Voting is closed. The answers are still here to read.'),
    ).toBeVisible({ timeout: 20_000 })
    await expect(guestPage.getByRole('button', { name: "Edit Edit Guest's answer" })).toHaveCount(0)
    await expect(guestPage.getByTestId('add-yourself-row')).toHaveCount(0)
  } finally {
    await guestContext.close()
  }
})
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/poll-edit.spec.ts`
Expected: `1 passed`. If the `Closed` assertion times out, check the server log for the `poll.deadline` job: `Update` must re-arm the deadline job when `deadlineAt` changes (internal/polls/service.go `syncDeadline`); a past `run_at` is claimed on the next tick.

- [ ] **Step 3: Commit**

```bash
git add e2e/poll-edit.spec.ts
git commit -m "test(e2e): cover editing a voted poll and a deadline closing it for a guest

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 10: `booking-create.spec.ts` — handle on /settings, page via /bookings/new, visitor books, reschedules, `.ics`, confirmation mail

**Files:**
- Create: `e2e/booking-create.spec.ts`

**Interfaces:**
- Consumes: `HandleField` (`#settings-handle`, button `Save handle`, toast `Handle saved`); `PageEditor` (`data-testid="page-editor"`, `#booking-title`, `#booking-slug` auto-slugified, `AvailabilityEditor` switches `aria-label="{Day} availability"` and time inputs `aria-label="{Day} start time"` / `"{Day} end time"`, button `Create page`, toast `Booking page created`, redirect to `/bookings/{id}` with `data-testid="booking-page-view"`); public page (`data-testid="booking-page"`, `slot-list`, chips named `{start} to {end}`, dialog with `#booking-name`, `#booking-email`, `Confirm booking`, `data-testid="booking-confirmed"`); plan D's `BookingConfirmed` `Add to calendar` link → `bookingCalendarIcsUrl` = `/api/v1/bookings/{id}/calendar.ics?t=…`; `ManageBooking` (`data-testid="manage-booking"`, `Reschedule` → dialog `Pick a new time` with its own `SlotPicker`, `Move booking`, toast `Booking moved`, `Add to calendar` link); mail subjects `Confirmed: {title}` and `Rescheduled: {title}` (plan D) and the manage link `/booking/{id}?t=…` inside the confirmation mail; Task 5's `waitForMail`/`extractLink`; Task 1's `pickFirstEnabledDayNotToday(scope)`.
- Produces: nothing consumed later.

- [ ] **Step 1: Create `e2e/booking-create.spec.ts`**

```ts
import { expect, pickFirstEnabledDayNotToday, signIn, test, waitForHydration } from './fixtures'
import { extractLink, waitForMail } from './mailpit'

// The editor takes the browser's zone for a new page; pinning it makes the slot arithmetic below
// (13:00–15:00 in 30-minute steps = 4 chips) hold on every machine. `browser.newContext()` does
// NOT inherit `test.use` options, so the visitor context sets the same zone explicitly.
test.use({ timezoneId: 'Europe/Oslo' })

test('an organiser sets a handle and creates a page; a visitor books, reschedules, downloads the .ics and gets the mails', async ({
  page,
  browser,
  request,
  user,
}) => {
  await signIn(page, user)

  // --- handle first: booking links start with it ---
  await page.goto('/settings')
  await waitForHydration(page)
  const handle = `e2e-${Date.now().toString(36)}`
  await page.locator('#settings-handle').fill(handle)
  await page.getByRole('button', { name: 'Save handle' }).click()
  await expect(page.getByText('Handle saved')).toBeVisible()
  await expect(page.getByText(`localhost:3100/book/${handle}`)).toBeVisible()

  // --- the page: one weekday window, Wednesday 13:00–15:00 ---
  await page.goto('/bookings/new')
  await waitForHydration(page)
  await expect(page.getByTestId('page-editor')).toBeVisible()
  await page.locator('#booking-title').fill('Office hours')
  await expect(page.locator('#booking-slug')).toHaveValue('office-hours')
  await expect(page.getByText(`localhost:3100/book/${handle}/office-hours`)).toBeVisible()

  for (const day of ['Monday', 'Tuesday', 'Thursday', 'Friday']) {
    await page.getByRole('switch', { name: `${day} availability` }).click()
    await expect(page.getByRole('switch', { name: `${day} availability` })).not.toBeChecked()
  }
  await expect(page.getByRole('switch', { name: 'Wednesday availability' })).toBeChecked()
  await page.getByLabel('Wednesday start time').fill('13:00')
  await page.getByLabel('Wednesday end time').fill('15:00')

  await page.getByRole('button', { name: 'Create page' }).click()
  await expect(page.getByText('Booking page created')).toBeVisible()
  await page.waitForURL(/\/bookings\/[^/?]+$/)
  await expect(page.getByTestId('booking-page-view')).toBeVisible()
  const pageId = new URL(page.url()).pathname.split('/').filter(Boolean).pop()!

  // --- a visitor books ---
  const visitorEmail = `booker-${Date.now()}@example.com`
  const visitorContext = await browser.newContext({ timezoneId: 'Europe/Oslo' })
  const visitorPage = await visitorContext.newPage()
  try {
    await visitorPage.goto(`/book/${handle}/office-hours`)
    await waitForHydration(visitorPage)
    await expect(visitorPage.getByTestId('booking-page')).toBeVisible()
    await expect(visitorPage.getByRole('heading', { name: 'Office hours' })).toBeVisible()

    // Only Wednesdays are enabled, so this lands on a Wednesday that is not today.
    await pickFirstEnabledDayNotToday(visitorPage)
    const slotList = visitorPage.getByTestId('slot-list')
    await expect(slotList).toBeVisible()
    await expect(slotList.getByRole('button')).toHaveCount(4)
    await expect(slotList.getByRole('button', { name: '13:00 to 13:30' })).toBeVisible()

    await slotList.getByRole('button', { name: '13:00 to 13:30' }).click()
    await expect(visitorPage.getByRole('dialog')).toBeVisible()
    await visitorPage.locator('#booking-name').fill('Booker One')
    await visitorPage.locator('#booking-email').fill(visitorEmail)
    await visitorPage.getByRole('button', { name: 'Confirm booking' }).click()

    const confirmed = visitorPage.getByTestId('booking-confirmed')
    await expect(confirmed).toBeVisible()
    await expect(confirmed).toContainText(visitorEmail)

    // Plan D: the card's "Add to calendar" points at the API .ics, not the dead SPA path.
    const cardIcs = await confirmed.getByRole('link', { name: 'Add to calendar' }).getAttribute('href')
    expect(cardIcs).toMatch(/^\/api\/v1\/bookings\/[^/?]+\/calendar\.ics\?t=.+/)
    const ics = await request.get(cardIcs!)
    expect(ics.status()).toBe(200)
    expect(ics.headers()['content-type']).toContain('text/calendar')
    expect(await ics.text()).toContain('BEGIN:VCALENDAR')

    // --- the confirmation mail carries the manage link ---
    const confirmation = await waitForMail(request, visitorEmail, { subject: /^Confirmed: Office hours/ })
    expect(confirmation.Text).toContain('Booker One')
    const manageLink = extractLink(confirmation, '/booking/')
    expect(manageLink.searchParams.get('t')).toBeTruthy()

    // --- reschedule from the manage page ---
    await visitorPage.goto(manageLink.pathname + manageLink.search)
    await waitForHydration(visitorPage)
    await expect(visitorPage.getByTestId('manage-booking')).toBeVisible()
    await expect(visitorPage.getByText('Confirmed', { exact: true })).toBeVisible()

    await visitorPage.getByRole('button', { name: 'Reschedule' }).click()
    const dialog = visitorPage.getByRole('dialog', { name: 'Pick a new time' })
    await expect(dialog).toBeVisible()
    await pickFirstEnabledDayNotToday(dialog)
    const dialogSlots = dialog.getByTestId('slot-list')
    await expect(dialogSlots).toBeVisible()
    // The booked 13:00 chip is gone from this day; 13:30 is the first free one now.
    await dialogSlots.getByRole('button', { name: '13:30 to 14:00' }).click()
    await dialog.getByRole('button', { name: 'Move booking' }).click()
    await expect(visitorPage.getByText('Booking moved')).toBeVisible()
    await expect(dialog).toBeHidden()
    await expect(visitorPage.getByTestId('manage-booking')).toContainText('13:30')

    // --- the manage page's .ics is a real calendar file for the moved booking ---
    const manageIcs = await visitorPage
      .getByRole('link', { name: 'Add to calendar' })
      .getAttribute('href')
    expect(manageIcs).toMatch(/^\/api\/v1\/bookings\/[^/?]+\/calendar\.ics\?t=.+/)
    const movedIcs = await request.get(manageIcs!)
    expect(movedIcs.status()).toBe(200)
    expect(movedIcs.headers()['content-type']).toContain('text/calendar')
    expect(await movedIcs.text()).toContain('BEGIN:VCALENDAR')

    await waitForMail(request, visitorEmail, { subject: /^Rescheduled: Office hours/ })
  } finally {
    await visitorContext.close()
  }

  // --- the organiser sees the booking on the page's own view, and got the organiser mail ---
  await page.goto(`/bookings/${pageId}`)
  await waitForHydration(page)
  await expect(page.getByTestId('booking-page-view')).toBeVisible()
  await expect(page.getByText('Booker One')).toBeVisible()
  await waitForMail(request, user.email, { subject: /^New booking: Office hours/ })
})
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/booking-create.spec.ts`
Expected: `1 passed`. If the `toHaveCount(4)` assertion fails on a Wednesday afternoon, `pickFirstEnabledDayNotToday` picked today — re-read its doc comment; it must never do so.

- [ ] **Step 3: Commit**

```bash
git add e2e/booking-create.spec.ts
git commit -m "test(e2e): create a booking page through the UI, book, reschedule, download .ics, read the mails

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 11: `invitations.spec.ts` — owner invites by API, invitee accepts from the mail, lands in the joined org, creates a poll there

**Files:**
- Create: `e2e/invitations.spec.ts`

**Interfaces:**
- Consumes: Limen `POST /api/v1/auth/organizations/invitations` body `{ "email": string, "role": "member" }` (plugins/organization `CreateInvitationRequest`; protected; Limen's CSRF check wants `Content-Type: application/json`, and our `CheckOrigin` wants an `Origin` header on mutating `/api/v1` calls — `page.request.post` sends neither Origin nor cookies' page context, so set `origin` explicitly); the invitation mail (subject `{inviter} invited you to {org} on whenweall`, link `/accept-invitation/{token}`); `AcceptInvitationCard` (heading `Join the organization`, button `Accept invitation`, then `/dashboard`); plan A's `switchOrganization` after accept and `GET /api/v1/auth/organizations/active` → `{ name, slug }`; plan A's org switcher inside the `Account menu` dropdown (assumption: the menu lists each organization by name — asserted with `getByRole('menu').getByText(orgName)`, the active-state marker is asserted through the API rather than the UI because plan A did not fix its markup); `ListMine` is per organization (`ListPollsByOrg`), so the owner's dashboard lists the invitee's poll.
- Produces: nothing consumed later.

- [ ] **Step 1: Create `e2e/invitations.spec.ts`**

```ts
import { APP_URL } from './e2e-env'
import { expect, pickTwoCalendarDays, signIn, test, waitForHydration } from './fixtures'
import { extractLink, waitForMail } from './mailpit'

test('an owner invites a teammate; the invitee accepts from the mail, is switched into the org and creates a poll there', async ({
  page,
  browser,
  request,
  user,
  userWithPoll,
}) => {
  const owner = userWithPoll
  const invitee = user

  await signIn(page, owner)

  // There is no invite UI in the SPA yet (only the accept side), so the owner invites through
  // Limen's own route, exactly the way the old billing.spec did. `page.request` carries the
  // owner's session cookies; the Origin header is what our CheckOrigin middleware requires on any
  // mutating /api/v1 call (a browser fetch() would add it on its own).
  const invite = await page.request.post('/api/v1/auth/organizations/invitations', {
    headers: { origin: APP_URL },
    data: { email: invitee.email, role: 'member' },
  })
  expect(invite.ok(), await invite.text()).toBe(true)

  const mail = await waitForMail(request, invitee.email, { subject: /invited you to .+ on whenweall$/ })
  const orgName = /invited you to (.+) on whenweall$/.exec(mail.Subject)![1]!
  expect(mail.Text).toContain(owner.name)
  const acceptLink = extractLink(mail, '/accept-invitation/')

  const inviteeContext = await browser.newContext()
  const inviteePage = await inviteeContext.newPage()
  try {
    await signIn(inviteePage, invitee)

    // Before accepting: the invitee's active org is their own personal one, not the inviter's.
    const before = (await (await inviteePage.request.get('/api/v1/auth/organizations/active')).json()) as {
      name: string
    }
    expect(before.name).not.toBe(orgName)

    await inviteePage.goto(acceptLink.pathname)
    await waitForHydration(inviteePage)
    await expect(inviteePage.getByRole('heading', { name: 'Join the organization' })).toBeVisible()
    await inviteePage.getByRole('button', { name: 'Accept invitation' }).click()
    await inviteePage.waitForURL('**/dashboard')

    // Plan A: accepting switches the active organization to the one just joined …
    const after = (await (await inviteePage.request.get('/api/v1/auth/organizations/active')).json()) as {
      name: string
    }
    expect(after.name).toBe(orgName)

    // … and the account menu's org switcher lists the joined org (by name) alongside the personal
    // one. Plan A left the active-state markup open, so the API check above is the authority on
    // "active"; this only asserts the switcher knows about the org.
    await inviteePage.getByRole('button', { name: 'Account menu' }).click()
    const menu = inviteePage.getByRole('menu')
    await expect(menu.getByText(orgName)).toBeVisible()
    await expect(menu.getByText(before.name)).toBeVisible()
    await inviteePage.keyboard.press('Escape')

    // The joined org's existing poll is now on the invitee's dashboard.
    await inviteePage.goto('/dashboard')
    await waitForHydration(inviteePage)
    await expect(
      inviteePage
        .locator('[data-testid="poll-card"]')
        .filter({ has: inviteePage.getByText('Seeded test poll', { exact: true }) }),
    ).toHaveCount(1)

    // A poll the invitee creates now belongs to the joined org …
    const title = `Team poll ${Date.now()}`
    await inviteePage.goto('/new')
    await waitForHydration(inviteePage)
    await inviteePage.locator('#creator-title').fill(title)
    await inviteePage.getByRole('button', { name: 'Next', exact: true }).click()
    await pickTwoCalendarDays(inviteePage)
    await inviteePage.getByRole('button', { name: 'Next', exact: true }).click()
    await inviteePage.getByRole('button', { name: 'Create poll' }).click()
    await inviteePage.waitForURL(/\/p\/[^/?]+/)
  } finally {
    await inviteeContext.close()
  }

  // … so the owner sees it on their own dashboard (polls are listed per organization).
  await page.goto('/dashboard')
  await waitForHydration(page)
  await expect(
    page
      .locator('[data-testid="poll-card"]')
      .filter({ has: page.getByText(/^Team poll \d+$/) }),
  ).toHaveCount(1)
})
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/invitations.spec.ts`
Expected: `1 passed`. If the POST returns 403 `bad_origin`, the `origin` header value must equal the server's `APP_URL` exactly (`http://localhost:3100`).

- [ ] **Step 3: Commit**

```bash
git add e2e/invitations.spec.ts
git commit -m "test(e2e): cover the invitation mail, accept, org switch and shared poll list

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 12: `settings.spec.ts` — rename, locale persistence, account deletion with password re-check

**Files:**
- Create: `e2e/settings.spec.ts`

**Interfaces:**
- Consumes: plan A's settings page — profile form (textbox labelled `Name`, button `Save`, toast `Name updated.`), the `LocaleSwitcher` inside `<main>` (group `Language`, buttons `EN`/`NO`; when signed in it also calls `updateProfile({locale})`), danger zone (button `Delete account`, dialog `Delete your account?`, field labelled `Password`, confirm button `Delete account`, error `Couldn't delete your account. Check your password and try again.`, toast `Account deleted.`); `GET /api/v1/auth/me` → `{ user: { name, locale, emailVerified, … } }` (plan A); `UserMenu` shows `user.name` in the dropdown label.
- Produces: nothing consumed later.

- [ ] **Step 1: Create `e2e/settings.spec.ts`**

```ts
import { expect, signIn, test, waitForHydration } from './fixtures'

type Me = { user: { name: string; locale: string } }

async function me(page: import('@playwright/test').Page): Promise<Me['user']> {
  const response = await page.request.get('/api/v1/auth/me')
  expect(response.status(), 'GET /api/v1/auth/me').toBe(200)
  return ((await response.json()) as Me).user
}

test('renaming shows in the header, the locale is stored on the profile, and deleting the account (password re-check) signs it out for good', async ({
  page,
  browser,
  user,
}) => {
  await signIn(page, user)
  await page.goto('/settings')
  await waitForHydration(page)
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()

  // --- display name ---
  const newName = `Renamed ${Date.now()}`
  // "Name" (settings_name_label); the handle field is labelled "Handle", so `exact` disambiguates.
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(newName)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Name updated.')).toBeVisible()
  await expect.poll(async () => (await me(page)).name).toBe(newName)

  await page.getByRole('button', { name: 'Account menu' }).click()
  await expect(page.getByRole('menu')).toContainText(newName)
  await page.keyboard.press('Escape')

  // --- locale: the switcher on /settings persists to the profile, not just the cookie ---
  const languageGroup = page.getByRole('main').getByRole('group', { name: 'Language' })
  await languageGroup.getByRole('button', { name: 'NO' }).click()
  await expect(page.locator('html')).toHaveAttribute('lang', 'nb', { timeout: 10_000 })
  await expect.poll(async () => (await me(page)).locale).toBe('nb')

  // A context with NO locale cookie, signed in as the same person, reads `nb` off the profile —
  // that is what makes e-mails and a second device follow the choice.
  const fresh = await browser.newContext()
  const freshPage = await fresh.newPage()
  try {
    await signIn(freshPage, user)
    const profile = await me(freshPage)
    expect(profile.locale).toBe('nb')
    expect(profile.name).toBe(newName)
  } finally {
    await fresh.close()
  }

  // Back to English so the danger-zone labels below match the en.json copy this spec asserts on.
  await page.getByRole('main').getByRole('group', { name: 'Språk' }).getByRole('button', { name: 'EN' }).click()
  await expect(page.locator('html')).toHaveAttribute('lang', 'en', { timeout: 10_000 })
  await expect.poll(async () => (await me(page)).locale).toBe('en')

  // --- delete account: wrong password is refused, the right one deletes ---
  await page.getByRole('button', { name: 'Delete account' }).click()
  const dialog = page.getByRole('dialog', { name: 'Delete your account?' })
  await expect(dialog).toBeVisible()
  await dialog.getByLabel('Password').fill('not-the-password-at-all')
  await dialog.getByRole('button', { name: 'Delete account' }).click()
  await expect(
    page.getByText("Couldn't delete your account. Check your password and try again."),
  ).toBeVisible()
  await expect(dialog).toBeVisible()

  await dialog.getByLabel('Password').fill(user.password)
  await dialog.getByRole('button', { name: 'Delete account' }).click()
  await expect(page.getByText('Account deleted.')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Sign in' })).toBeVisible()
  expect((await page.request.get('/api/v1/auth/me')).status()).toBe(401)

  // The credentials are gone, not just the session.
  await page.goto('/login')
  await waitForHydration(page)
  await page.locator('#login-email').fill(user.email)
  await page.locator('#login-password').fill(user.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText("That email or password isn't right.")).toBeVisible()
  await expect(page).toHaveURL(/\/login/)
})
```

Note on `Språk`: once the page is in Norwegian, the language group's accessible name is `nb.json`'s `locale_switch_label`. Confirm the value with `grep '"locale_switch_label"' web/messages/nb.json` and adjust the one string if it differs from `Språk`.

- [ ] **Step 2: Run**

Run: `grep '"locale_switch_label"' web/messages/nb.json && bunx playwright test e2e/settings.spec.ts`
Expected: the grep prints the nb label used above; `1 passed`.

- [ ] **Step 3: Commit**

```bash
git add e2e/settings.spec.ts
git commit -m "test(e2e): cover rename, profile locale persistence and password-checked account deletion

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 13: `admin.spec.ts` additions — lock / unlock / delete with reasons, audit rows, jobs page with retry

**Files:**
- Modify: `e2e/admin.spec.ts` (append two tests inside the existing `test.describe('admin console', …)`)

**Interfaces:**
- Consumes: Task 4's `userStaffWithFailedJob` fixture (`failedJobId`, `last_error` contains the id); plan E's `users.$id.tsx` actions through `ReasonDialog` (reason input labelled `Why are you doing this?`; confirm button disabled until a reason is typed) — **assumption**: the action and confirm buttons are labelled starting with `Lock`, `Unlock` and `Delete` (plan E did not fix the exact keys; the regexes `/^Lock\b/`, `/^Unlock\b/`, `/^Delete\b/` cover `Lock` / `Lock account` / `Lock user`); badge `Locked`; `admin_user_not_found` `No account with that id.`; audit actions `lock-user`, `unlock-user`, `delete-user`, `job.retry` (internal/admin/audit.go) on `/admin/audit` rows with the typed reason; plan E's `/admin/jobs` route reachable from a nav tab labelled `Failed jobs` (plan E key `admin_nav_jobs`) listing `kind`, `attempts`, `lastError`, `runAt` with a per-row button starting with `Retry`; plan A's locked-user handling — a locked user's `GET /api/v1/auth/me` answers 403 (`LockedSessionMiddleware`) and the SPA renders them signed out.
- Produces: nothing consumed later.

- [ ] **Step 1: Append to the `describe` block in `e2e/admin.spec.ts`** (before its closing `})`)

```ts
  test('staff lock, unlock and delete a user with reasons; the user is signed out while locked; the audit log records each', async ({
    page,
    browser,
    userStaff,
    user,
  }) => {
    // The target signs in first so the lock can be seen taking effect on a live session.
    const targetContext = await browser.newContext()
    const targetPage = await targetContext.newPage()
    try {
      await signIn(targetPage, user)

      await signIn(page, userStaff)
      await page.goto(`/admin/users?q=${encodeURIComponent(user.email)}`)
      await waitForHydration(page)
      await expect(page.getByRole('cell', { name: user.email })).toBeVisible()
      await page.getByRole('link', { name: user.name }).click()
      await page.waitForURL(/\/admin\/users\/[^/?]+$/)
      const userId = new URL(page.url()).pathname.split('/').filter(Boolean).pop()!
      await expect(page.getByRole('heading', { name: user.name })).toBeVisible()

      // Plan E's action buttons and ReasonDialog confirm buttons: matched on their leading verb
      // (Lock / Unlock / Delete) since the plan left the exact label open.
      async function actWithReason(verb: RegExp, reason: string) {
        await page.getByRole('button', { name: verb }).click()
        const dialog = page.getByRole('dialog')
        await expect(dialog).toBeVisible()
        const confirm = dialog.getByRole('button', { name: verb })
        await expect(confirm).toBeDisabled() // a reason is mandatory
        await dialog.getByLabel('Why are you doing this?').fill(reason)
        await confirm.click()
        await expect(dialog).toBeHidden()
      }

      // --- lock ---
      await actWithReason(/^Lock\b/, 'e2e: lock')
      await expect(page.getByText('Locked', { exact: true }).first()).toBeVisible()
      await expect(page.getByText('e2e: lock')).toBeVisible() // lockReason on the detail card

      // The locked user's very next request is refused, and the SPA shows them signed out.
      await expect
        .poll(async () => (await targetPage.request.get('/api/v1/auth/me')).status())
        .toBe(403)
      await targetPage.reload()
      await waitForHydration(targetPage)
      await expect(targetPage.getByRole('link', { name: 'Sign in' })).toBeVisible()
      await expect(targetPage.getByRole('button', { name: 'Account menu' })).toHaveCount(0)

      // --- unlock ---
      await actWithReason(/^Unlock\b/, 'e2e: unlock')
      await expect(page.getByText('Locked', { exact: true })).toHaveCount(0)
      await targetPage.context().clearCookies()
      await signIn(targetPage, user) // works again
      await expect(targetPage.getByRole('heading', { name: 'Your polls' })).toBeVisible()

      // --- delete ---
      await actWithReason(/^Delete\b/, 'e2e: delete')
      await page.goto(`/admin/users/${userId}`)
      await waitForHydration(page)
      await expect(page.getByText('No account with that id.')).toBeVisible()

      await targetPage.goto('/login')
      await waitForHydration(targetPage)
      await targetPage.locator('#login-email').fill(user.email)
      await targetPage.locator('#login-password').fill(user.password)
      await targetPage.getByRole('button', { name: 'Sign in' }).click()
      await expect(targetPage.getByText("That email or password isn't right.")).toBeVisible()

      // --- audit log ---
      await page.goto('/admin/audit')
      await waitForHydration(page)
      for (const [action, reason] of [
        ['lock-user', 'e2e: lock'],
        ['unlock-user', 'e2e: unlock'],
        ['delete-user', 'e2e: delete'],
      ] as const) {
        await expect(
          page.getByRole('row').filter({ hasText: action }).filter({ hasText: reason }),
        ).toHaveCount(1)
      }
    } finally {
      await targetContext.close()
    }
  })

  test('the jobs page lists a dead-lettered job and retrying it clears it from the list', async ({
    page,
    userStaffWithFailedJob,
  }) => {
    const { failedJobId } = userStaffWithFailedJob
    await signIn(page, userStaffWithFailedJob)
    await page.goto('/admin')
    await waitForHydration(page)

    // The dashboard counts it …
    const failedCard = page.getByText('Failed jobs').locator('..')
    await expect(failedCard).not.toContainText(/^Failed jobs\s*0$/)

    // … and the Jobs tab (plan E) lists it: the seed embeds the job id in last_error so this row
    // is unambiguous even when other runs left dead letters behind.
    await page.getByRole('link', { name: 'Failed jobs' }).click()
    await page.waitForURL('**/admin/jobs')
    const row = page.getByRole('row').filter({ hasText: failedJobId })
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('mail:send')
    await expect(row).toContainText('e2e: seeded dead-lettered job')

    await row.getByRole('button', { name: /^Retry\b/ }).click()
    // Retry resets attempts to 0, so the row leaves the dead-letter view.
    await expect(row).toHaveCount(0)

    await page.goto('/admin/audit')
    await waitForHydration(page)
    await expect(
      page.getByRole('row').filter({ hasText: 'job.retry' }).filter({ hasText: failedJobId }),
    ).toHaveCount(1)
  })
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/admin.spec.ts`
Expected: `6 passed`. If the `/^Lock\b/` locator finds nothing, run `grep -n 'admin_action\|admin_lock\|admin_unlock\|admin_delete' web/messages/en.json` and align the three regexes with plan E's actual labels — change only the regexes, not the flow.

- [ ] **Step 3: Commit**

```bash
git add e2e/admin.spec.ts
git commit -m "test(e2e): cover admin lock/unlock/delete with reasons, the audit log and the jobs page retry

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 14: `poll-flow.spec.ts` — the guest leaves an e-mail, the finalize toast reports the count, the "decided" mail arrives

**Files:**
- Modify: `e2e/poll-flow.spec.ts` (imports; guest vote step; finalize step; add a mail assertion)

**Interfaces:**
- Consumes: plan C's `POST /polls/{id}/finalize` → `{ sent: n }` and `FinalizeDialog`'s toast `Decided. {count} people were notified.`; `AnswerForm`'s guest e-mail input (`aria-label="Email"`, desktop layout — the phone layout's twin is `display:none`, which the role engine ignores); mail subject `{title} is decided`; Task 5's `waitForMail`.
- Produces: nothing consumed later.

- [ ] **Step 1: Edit `e2e/poll-flow.spec.ts`**

Imports:
```diff
 import {
   expect,
   pickTwoCalendarDays,
   signIn,
   test,
   waitForHydration,
 } from './fixtures'
+import { waitForMail } from './mailpit'
```

Guest vote step — give the guest an address:
```diff
     const guestName = `Guest ${Date.now()}`
+    const guestEmail = `guest-${Date.now()}@example.com`
     await guestPage.getByTestId('add-yourself-row').getByLabel('Your name').fill(guestName)
+    await guestPage.getByRole('textbox', { name: 'Email' }).fill(guestEmail)
     const guestCells = guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
```

Finalize step — assert the toast's count (plan C restored `sent`; before it the toast read "undefined people"):
```diff
     await finalizeDialog.getByRole('button', { name: 'Confirm the pick' }).click()
     await expect(finalizeDialog).toBeHidden()
+
+    // Plan C: the response carries `sent` (unique recipients enqueued) and the toast prints it.
+    // The guest left an address, so at least one person was notified — never "undefined".
+    const toast = page.getByText(/^Decided\. \d+ people were notified\.$/)
+    await expect(toast).toBeVisible()
+    const notified = Number(/\d+/.exec((await toast.textContent()) ?? '')?.[0])
+    expect(notified).toBeGreaterThanOrEqual(1)
```

After the guest reload assertion, still inside the `try` (before `finally`):
```diff
     await guestPage.reload()
     await expect(guestPage.getByTestId('finalized-banner')).toBeVisible()
+
+    // The guest's "decided" mail is really sent, with the poll's title in the subject.
+    const decided = await waitForMail(request, guestEmail, { subject: /is decided$/ })
+    expect(decided.Subject).toBe(`${title} is decided`)
+    expect(decided.Text).toContain(guestName)
```

- [ ] **Step 2: Run**

Run: `bunx playwright test e2e/poll-flow.spec.ts`
Expected: `1 passed`.

- [ ] **Step 3: Commit**

```bash
git add e2e/poll-flow.spec.ts
git commit -m "test(e2e): assert the finalize toast count and the decided mail in the poll journey

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 15: Image mode — `compose.e2e.yaml` overlay, env file, helper scripts, CI `e2e-image` job

**Files:**
- Create: `compose.e2e.yaml`
- Create: `e2e/compose.e2e.env`
- Create: `e2e/compose-e2e.sh` (executable)
- Create: `e2e/assert-hardening.sh` (executable)
- Modify: `.github/workflows/ci.yml` (append the `e2e-image` job; the existing `e2e` job stays)

**Interfaces:**
- Consumes: `compose.yaml` (plan B: `app` has `image: ghcr.io/refsdal/whenweall:latest` + `build:`, `read_only`, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`, `${APP_PORT:-3000}:3000`, `${POSTGRES_PORT:-5433}`, required `POSTGRES_PASSWORD`/`AUTH_SECRET`/`SMTP_HOST`); the Dockerfile's `HEALTHCHECK CMD ["/whenweall","healthcheck"]` and `USER 65532:65532`; Task 1's `SERVER_MODE`/`reuseExistingServer`; Task 3's image branch of `run-server.sh`.
- Produces: `e2e/compose-e2e.sh <compose args…>` = `docker compose --env-file e2e/compose.e2e.env -f compose.yaml -f compose.e2e.yaml "$@"` (project `whenweall-e2e`, app on :3100, Mailpit HTTP on :8026, Postgres on :5435); local image `whenweall:e2e`; `e2e/assert-hardening.sh` exits non-zero unless the running app container is read-only, `CapDrop=[ALL]`, `no-new-privileges:true`, user `65532:65532` and healthy.

- [ ] **Step 1: Create `compose.e2e.yaml`**

```yaml
# Overlay for running the Playwright suite against the BUILT image with compose's hardening flags —
# what CI's `e2e-image` job does, and what `E2E_SERVER=image bunx playwright test` expects locally:
#
#   docker build -t whenweall:e2e .
#   e2e/compose-e2e.sh up -d --wait          # = docker compose --env-file e2e/compose.e2e.env \
#                                            #     -f compose.yaml -f compose.e2e.yaml up -d --wait
#   e2e/assert-hardening.sh                  # read_only / cap_drop / no-new-privileges / user / healthy
#   E2E_SERVER=image bunx playwright test
#   e2e/compose-e2e.sh down -v
#
# Everything security-relevant (read_only, cap_drop ALL, no-new-privileges, the scratch image's
# USER) is inherited from compose.yaml on purpose: this file must never weaken it, only add the
# test-only pieces — the seed route, a Mailpit sink, the prebuilt local image, and an explicit
# `user` so a Dockerfile regression that drops USER would still be caught by assert-hardening.sh
# rather than silently running as root. Ports/credentials come from e2e/compose.e2e.env.
services:
  db:
    # Nothing to override: a throwaway database per run — `down -v` removes the volume.
    image: postgres:18-alpine

  mailpit:
    # Same pinned tag as e2e/e2e-env.ts's MAILPIT_IMAGE; keep them in sync.
    image: axllent/mailpit:v1.31.0
    ports:
      - '127.0.0.1:1026:1025' # SMTP (the app talks to it as mailpit:1025 inside the network)
      - '127.0.0.1:8026:8025' # HTTP API, read by e2e/mailpit.ts

  app:
    # The image the CI job (or you) just built — never pulled from ghcr, never rebuilt here.
    image: whenweall:e2e
    pull_policy: never
    # Belt-and-braces alongside the Dockerfile's own USER 65532:65532.
    user: '65532:65532'
    # A crash should fail the run loudly, not restart into a green-looking healthcheck.
    restart: 'no'
    depends_on:
      mailpit:
        condition: service_started
    environment:
      # The one test-only switch: mounts POST /api/test/seed. config.Load refuses it alongside
      # APP_ENV=production, which is why compose.e2e.env sets APP_ENV=test.
      ENABLE_TEST_ROUTES: 'true'
      # Explicitly off (compose.yaml would also leave them empty): the suite covers the documented
      # captcha-off default and must not reach challenges.cloudflare.com.
      TURNSTILE_SITE_KEY: ''
      TURNSTILE_SECRET_KEY: ''
```

- [ ] **Step 2: Create `e2e/compose.e2e.env`**

```dotenv
# Interpolation values for compose.yaml + compose.e2e.yaml in image mode (loaded with
# `docker compose --env-file e2e/compose.e2e.env …`, see e2e/compose-e2e.sh). Nothing here is a
# secret: the database lives for one test run. AUTH_SECRET duplicates e2e/e2e-env.ts on purpose
# (compose cannot read TypeScript) — keep the two in sync.
COMPOSE_PROJECT_NAME=whenweall-e2e
APP_ENV=test
APP_URL=http://localhost:3100
APP_PORT=3100
# 5435, not 5434: never collide with a go-mode `whenweall-e2e-db` container left running.
POSTGRES_PORT=5435
POSTGRES_PASSWORD=e2e-test-password
AUTH_SECRET=e2e-not-a-real-secret-YuerLHH9iaTZviHi0y2hkpP
SMTP_HOST=mailpit
SMTP_PORT=1025
SMTP_SECURE=false
EMAIL_FROM=whenweall e2e <no-reply@localhost>
TRUST_PROXY=false
DATABASE_POOL_SIZE=10
MIGRATE_ON_BOOT=true
```

- [ ] **Step 3: Create `e2e/compose-e2e.sh`**

```bash
#!/usr/bin/env bash
# The one spelling of the image-mode compose invocation, so CI, the README and a developer's shell
# can't drift: `e2e/compose-e2e.sh up -d --wait`, `e2e/compose-e2e.sh logs app`,
# `e2e/compose-e2e.sh down -v`, …
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
exec docker compose --env-file e2e/compose.e2e.env -f compose.yaml -f compose.e2e.yaml "$@"
```

- [ ] **Step 4: Create `e2e/assert-hardening.sh`**

```bash
#!/usr/bin/env bash
# Proves the compose hardening claims are LIVE on the running app container, not just written in
# compose.yaml: read-only root filesystem, every capability dropped, no-new-privileges, the
# unprivileged user, and a passing Docker HEALTHCHECK (`/whenweall healthcheck` — the scratch image
# has no shell, so this exercises the binary's own subcommand). Run after `compose-e2e.sh up -d
# --wait`; exits non-zero on the first claim that does not hold.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

app="$(e2e/compose-e2e.sh ps -q app)"
if [ -z "$app" ]; then
  echo "assert-hardening: no running app container (run e2e/compose-e2e.sh up -d --wait first)" >&2
  exit 1
fi

check() {
  local label="$1" want="$2" got="$3"
  if [ "$got" != "$want" ]; then
    echo "assert-hardening: $label = '$got', want '$want'" >&2
    exit 1
  fi
  echo "assert-hardening: $label = '$got' OK"
}

check "ReadonlyRootfs"      "true"          "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$app")"
check "CapDrop"             '["ALL"]'       "$(docker inspect -f '{{json .HostConfig.CapDrop}}' "$app")"
check "SecurityOpt"         '["no-new-privileges:true"]' "$(docker inspect -f '{{json .HostConfig.SecurityOpt}}' "$app")"
check "User"                "65532:65532"   "$(docker inspect -f '{{.Config.User}}' "$app")"
check "Health"              "healthy"       "$(docker inspect -f '{{.State.Health.Status}}' "$app")"
check "ENABLE_TEST_ROUTES"  "true"          "$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$app" | sed -n 's/^ENABLE_TEST_ROUTES=//p')"

# And the seed route the suite depends on is actually reachable through the published port.
status="$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -H 'Origin: http://localhost:3100' -d '{}' http://localhost:3100/api/test/seed)"
check "POST /api/test/seed" "200" "$status"
```

Then: `chmod +x e2e/compose-e2e.sh e2e/assert-hardening.sh`.

- [ ] **Step 5: Bring the stack up locally and run the suite in image mode**

Run:
```bash
docker build -t whenweall:e2e . \
  && e2e/compose-e2e.sh up -d --wait --wait-timeout 120 \
  && e2e/assert-hardening.sh \
  && E2E_SERVER=image bunx playwright test; status=$?
e2e/compose-e2e.sh logs --no-color app | tail -20
e2e/compose-e2e.sh down -v
exit $status
```
Expected: `assert-hardening` prints six `OK` lines plus `POST /api/test/seed = '200' OK`; Playwright reports `N passed` (same total as go mode) with `run-server.sh` never having started containers (`docker ps` shows only `whenweall-e2e-app-1`, `whenweall-e2e-db-1`, `whenweall-e2e-mailpit-1` during the run); no `e2e/.run-server-marker.json` is left behind; `down -v` removes the three containers and the volume.

- [ ] **Step 6: Add the `e2e-image` job to `.github/workflows/ci.yml`** (append after the `e2e` job; keep `e2e` unchanged)

```yaml
  # The same Playwright suite, this time against the built Docker image running under compose's
  # hardening flags (read_only, cap_drop ALL, no-new-privileges, USER 65532, the binary's own
  # HEALTHCHECK) — spec §9's "runs in CI against the real built Docker image + Postgres — proves
  # the port and keeps the hardening claims honest". `e2e` above keeps covering the fast `go run`
  # path; this job proves the FROM-scratch image (tzdata, CA bundle, no shell) is what passes.
  e2e-image:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: oven-sh/setup-bun@v2
        with: { bun-version-file: web/.bun-version }
      - uses: actions/cache@v4
        with:
          {
            path: ~/.bun/install/cache,
            key: "bun-e2e-image-${{ runner.os }}-${{ hashFiles('bun.lock') }}",
            restore-keys: 'bun-e2e-image-${{ runner.os }}-',
          }
      # Only the root package (Playwright) — the SPA is built inside the Docker image below.
      - run: bun install --frozen-lockfile
      - id: pw
        run: echo "version=$(bun pm ls | grep -o '@playwright/test@[^ ]*' | head -1)" >> "$GITHUB_OUTPUT"
      - uses: actions/cache@v4
        id: pw-cache
        with:
          {
            path: ~/.cache/ms-playwright,
            key: 'playwright-${{ runner.os }}-${{ steps.pw.outputs.version }}',
          }
      - if: steps.pw-cache.outputs.cache-hit == 'true'
        run: bunx playwright install-deps chromium
      - if: steps.pw-cache.outputs.cache-hit != 'true'
        run: bunx playwright install --with-deps chromium
      - name: Build the release image
        run: docker build -t whenweall:e2e .
      - name: Start Postgres + Mailpit + the image with the hardening flags
        run: e2e/compose-e2e.sh up -d --wait --wait-timeout 120
      - name: Assert the hardening flags are live
        run: e2e/assert-hardening.sh
      - name: Playwright against the image
        run: bunx playwright test
        env: { E2E_SERVER: image }
      - name: App logs
        if: failure()
        run: |
          mkdir -p playwright-report
          e2e/compose-e2e.sh logs --no-color app > playwright-report/app.log || true
          e2e/compose-e2e.sh ps > playwright-report/compose-ps.txt || true
      - name: Tear the stack down
        if: always()
        run: e2e/compose-e2e.sh down -v --remove-orphans
      - uses: actions/upload-artifact@v7
        if: failure()
        with: { name: playwright-report-image, path: playwright-report, retention-days: 7 }
```

- [ ] **Step 7: Validate the workflow file**

Run: `docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest -color .github/workflows/ci.yml 2>&1 | head -20; docker compose --env-file e2e/compose.e2e.env -f compose.yaml -f compose.e2e.yaml config --quiet && echo compose-config-ok`
Expected: actionlint prints nothing (or is skipped if the image cannot be pulled — then at least `python3 -c 'import yaml,sys; yaml.safe_load(open(".github/workflows/ci.yml"))' && echo yaml-ok` must print `yaml-ok`); `compose-config-ok` is printed.

- [ ] **Step 8: Commit**

```bash
git add compose.e2e.yaml e2e/compose.e2e.env e2e/compose-e2e.sh e2e/assert-hardening.sh .github/workflows/ci.yml
git commit -m "ci: run Playwright against the built image under compose's hardening flags

compose.e2e.yaml overlays compose.yaml with the prebuilt whenweall:e2e image,
ENABLE_TEST_ROUTES, a pinned Mailpit and an explicit user 65532; the e2e-image
job builds the image, brings the stack up with --wait, asserts read_only /
cap_drop ALL / no-new-privileges / user / healthy on the running container, and
runs the same suite with E2E_SERVER=image. The go-run e2e job stays.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 16: README Testing/Development and CONTRIBUTING for the new suite shape

**Files:**
- Modify: `README.md` (Development section's e2e paragraph; the whole "End-to-end" bullet and "Running just one thing" block in Testing)
- Modify: `CONTRIBUTING.md` ("Before opening a pull request" e2e paragraph)

**Interfaces:**
- Consumes: everything above (port 3100, image mode commands, spec list).
- Produces: nothing.

- [ ] **Step 1: README — Development section**

Replace the paragraph + code block that begins `End-to-end tests (Playwright, the oracle for this whole rewrite) run against the real` with:

````markdown
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
docker build -t whenweall:e2e .
e2e/compose-e2e.sh up -d --wait      # compose.yaml + compose.e2e.yaml, app on :3100
e2e/assert-hardening.sh              # proves the flags are live on the running container
E2E_SERVER=image bunx playwright test
e2e/compose-e2e.sh down -v
```
````

- [ ] **Step 2: README — Testing section**

Replace the `**End-to-end** (…)` bullet with:

```markdown
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
```

Replace the "Running just one thing" block with:

```bash
go test ./internal/bookings/...
go test -race -run 'Race|Concurrent' ./internal/polls/... ./internal/bookings/...
cd web && bunx vitest run src/components/poll/__tests__/VoteGrid.test.tsx
bunx playwright test e2e/poll-flow.spec.ts
bunx playwright test e2e/auth-email.spec.ts     # the Mailpit-backed flows
E2E_SERVER=image bunx playwright test e2e/smoke.spec.ts   # against the running image stack
bunx playwright test --ui   # interactive
```

- [ ] **Step 3: CONTRIBUTING — e2e paragraph**

Replace the paragraph + block that begins `End-to-end tests need a browser once per machine` with:

````markdown
End-to-end tests need a browser once per machine, then run against the real built SPA
served by the real Go binary on `:3100`, with a throwaway Postgres and Mailpit (see
[`e2e/run-server.sh`](./e2e/run-server.sh)). Specs that involve e-mail read the inbox through
[`e2e/mailpit.ts`](./e2e/mailpit.ts); fixtures come from `POST /api/test/seed`
(`internal/httpserver/testroutes.go`) and fail loudly rather than skip when the seed is
incomplete. Turnstile is off in this harness.

```bash
bunx playwright install --with-deps chromium
bunx playwright test
```

CI also runs the suite against the built Docker image (`e2e-image` job). To reproduce that
locally: `docker build -t whenweall:e2e . && e2e/compose-e2e.sh up -d --wait &&
e2e/assert-hardening.sh && E2E_SERVER=image bunx playwright test; e2e/compose-e2e.sh down -v`.

New e2e specs: one journey per file, role-based locators (`getByRole`/`getByLabel`), auto-waiting
`expect(...)` over fixed sleeps, `expect.poll` for anything that arrives through the jobs worker
(mail), and a fresh `browser.newContext()` per additional person in the story.
````

- [ ] **Step 4: Verify the docs point at things that exist**

Run: `grep -n '3000' README.md | grep -i 'playwright\|e2e'; grep -n 'compose-e2e.sh\|assert-hardening.sh\|E2E_SERVER' README.md CONTRIBUTING.md | wc -l; test -x e2e/compose-e2e.sh && test -x e2e/assert-hardening.sh && echo scripts-executable`
Expected: the first grep prints nothing; the count is at least 6; `scripts-executable`.

- [ ] **Step 5: Commit**

```bash
git add README.md CONTRIBUTING.md
git commit -m "docs: describe the e2e suite's coverage, :3100 harness and image-mode run

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

## Assumptions about plans A–E's UI (verify while executing; change only the named locator)

| Spec | Assumption | Source | If wrong, change |
| --- | --- | --- | --- |
| auth-email | Unverified sign-in shows text `Your email isn't verified yet.` and a button `Resend verification email`; success text starts `Verification email sent` | keys `auth_login_unverified`, `auth_resend_verification`, `auth_verify_resent` / `auth_verify_resend_success` (en.json) | the three strings in Task 5 |
| auth-email | `/verify-email?token=…` ends on the done card with heading `Email verified` | key `auth_verify_done_title`; plan A brief item 1 | one heading string |
| settings | Profile form: textbox `Name`, button `Save`, toast `Name updated.`; danger zone: `Delete account`, dialog `Delete your account?`, field `Password`, error/success copy | keys `settings_name_*`, `settings_delete_*` (en.json, already present) | strings in Task 12 |
| settings | `GET /api/v1/auth/me` returns `{ user: { name, locale } }` | contract "GET /api/v1/auth/me response gains emailVerified, locale, name" | the `Me` type |
| invitations | Account menu lists organisations by name (any menu item/label role) | contract "UserMenu lists orgs (`listOrganizations()`) with a switch action" | `menu.getByText(orgName)` |
| admin | Lock/Unlock/Delete buttons and ReasonDialog confirm labels begin with `Lock`/`Unlock`/`Delete`; jobs nav tab `Jobs`; per-row `Retry` button | plan E brief items 1–2 (labels not fixed) | the four regexes in Task 13 |
| admin | Locked user's `GET /api/v1/auth/me` → 403 and the SPA renders signed-out | `LockedSessionMiddleware` (403 `forbidden`) + plan A item 9 | none — this is code, not copy |
| poll-edit | `Update` with a past `deadlineAt` re-arms `poll.deadline`, which closes the poll on the next worker claim | internal/polls/service.go `syncDeadline` (create) — confirm the update path does the same | if not, close via `Close voting` and drop the deadline half |
| booking-create | `BookingConfirmed`'s `Add to calendar` href is `/api/v1/bookings/{id}/calendar.ics?t=…` | plan D item 1 (`bookingCalendarIcsUrl`) | the two `toMatch` regexes |
| creator/poll pages | Non-staff hitting `/api/v1/admin/*` get 403 (not the brief's "404") | `auth.Service.RequireStaff` writes 403 `forbidden`; the SPA route shows the 404 card | none |

## Self-review (done while writing; left here for the executor)

- Spec coverage: harness (Tasks 1–3), seed (4), mail flows (5), comments (6), signed-in vote (7), creator (8), edit/deadline (9), booking creation (10), invitations (11), settings (12), admin lock/unlock/delete/audit/jobs (13), finalize toast (14), image mode + CI (15), docs (16). The brief's "OIDC button" is not e2e-able (no IdP in the harness) and is intentionally absent.
- Type consistency: `waitForMail(request, to, { subject, minCount, timeout })` / `extractLink(message, pathPrefix)` are used identically in Tasks 5, 10, 11, 14; `pickFirstEnabledDayNotToday(scope: Page | Locator)` is defined in Task 1 and used with a `Locator` in Task 10; `RegisterTestRoutes(mux, cfg, sqlDB, authSvc, polls, bookings)` matches Task 4's test helper and `main.go` call; `userStaffWithFailedJob.failedJobId` is produced by Task 1's fixture type + Task 4's route and consumed in Task 13.
- No placeholders: every code step carries the full file or the exact diff; every run step names a command and its expected output.
