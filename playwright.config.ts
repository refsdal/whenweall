import { defineConfig, devices } from '@playwright/test'
import {
  AUTH_SECRET,
  DB_CONTAINER,
  DB_NAME,
  DB_PASSWORD,
  DB_PORT,
  DB_USER,
  MAILPIT_CONTAINER,
  MAILPIT_HTTP_PORT,
  MAILPIT_SMTP_PORT,
  TURNSTILE_SECRET_KEY,
  TURNSTILE_SITE_KEY,
} from './e2e/e2e-env'

/**
 * The suite runs the real Go backend (cmd/whenweall) with the SPA it serves built and copied into
 * its embedded dist/ — see `webServer.command` below — rather than a dev server: `live.spec.ts`
 * and `booking.spec.ts` need a real WebSocket upgrade the way a production deployment actually
 * handles it, and the built SPA is what internal/httpserver/spa.go actually serves in every real
 * environment (see Dockerfile's own `web` build stage).
 *
 * Postgres and Mailpit run as two throwaway Docker containers, started by `e2e/run-server.sh`
 * (webServer's own command — see its doc comment for why container startup lives there rather
 * than in Playwright's `globalSetup` hook) and stopped in `globalTeardown` once the whole suite
 * finishes.
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
  use: { baseURL: 'http://localhost:3000', locale: 'en-US', trace: 'on-first-retry' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  globalTeardown: './e2e/global-teardown.ts',
  webServer: {
    command: 'bash e2e/run-server.sh',
    url: 'http://localhost:3000/healthz',
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
    env: {
      // Read by e2e/run-server.sh to start/probe the throwaway containers.
      DB_CONTAINER,
      DB_PORT: String(DB_PORT),
      DB_USER,
      DB_PASSWORD,
      DB_NAME,
      MAILPIT_CONTAINER,
      MAILPIT_SMTP_PORT: String(MAILPIT_SMTP_PORT),
      MAILPIT_HTTP_PORT: String(MAILPIT_HTTP_PORT),
      // Read by cmd/whenweall itself (internal/config.Load).
      ENABLE_TEST_ROUTES: 'true',
      APP_ENV: 'test',
      APP_URL: 'http://localhost:3000',
      PORT: '3000',
      DATABASE_URL: databaseURL,
      DATABASE_POOL_SIZE: '10',
      AUTH_SECRET: AUTH_SECRET,
      SMTP_HOST: 'localhost',
      SMTP_PORT: String(MAILPIT_SMTP_PORT),
      SMTP_SECURE: 'false',
      EMAIL_FROM: 'whenweall e2e <no-reply@localhost>',
      TURNSTILE_SITE_KEY: TURNSTILE_SITE_KEY,
      TURNSTILE_SECRET_KEY: TURNSTILE_SECRET_KEY,
      TRUST_PROXY: 'false',
      MIGRATE_ON_BOOT: 'true',
    },
  },
})
