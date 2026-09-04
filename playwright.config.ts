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
