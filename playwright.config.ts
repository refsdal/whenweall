import { defineConfig, devices } from '@playwright/test'

/**
 * The suite runs the *built* worker via `vite preview` rather than `vite dev`.
 *
 * `live.spec.ts` needs a real WebSocket upgrade on `/api/polls/:id/ws` to reach the worker.
 * Under `vite dev` it never does — the request isn't even logged by the dev server, because
 * Vite's own dev middleware intercepts the `Upgrade: websocket` header before it gets to the
 * Cloudflare Vite plugin's worker proxy. Under `vite preview` the plugin runs the actual built
 * worker in workerd/miniflare, and the same request correctly answers
 * `101 Switching Protocols` (verified with a raw WebSocket client against both `vite dev` and
 * `vite preview` while writing this config — see task-20-report.md for the transcript).
 * `.dev.vars` (ENABLE_TEST_ROUTES, TURNSTILE_SECRET_KEY, ...) is honoured by `vite preview` the
 * same way it is by `vite dev` — confirmed via `POST /api/test/seed`, which 404s unless
 * `ENABLE_TEST_ROUTES=true` is loaded.
 */

/**
 * `screenshots.spec.ts` only captures the marketing images in `docs/screenshots/` — it asserts
 * almost nothing and writes files into the working tree, so it stays out of the regular suite
 * and of CI. `bun run screenshots` sets `SCREENSHOTS=1` to opt it back in.
 */
const captureScreenshots = process.env.SCREENSHOTS === '1'

export default defineConfig({
  testDir: 'e2e',
  testIgnore: captureScreenshots ? undefined : '**/screenshots.spec.ts',
  timeout: 60_000,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  // A fixed browser locale keeps Paraglide's `preferredLanguage` strategy deterministic: without
  // it the suite resolves whatever `Accept-Language` the host machine happens to send, and the
  // English assertions in `i18n.spec.ts` pass or fail depending on the developer's OS settings.
  use: { baseURL: 'http://localhost:3000', locale: 'en-US', trace: 'on-first-retry' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  // Applies local D1 migrations before the webServer/tests start — see e2e/global-setup.ts.
  globalSetup: './e2e/global-setup.ts',
  webServer: {
    // `bun run` appends everything after `--` to the end of the script, so this expands to
    // `bun run build && vite preview --port 3000` — one definition of "preview", in package.json.
    command: 'bun run preview -- --port 3000',
    url: 'http://localhost:3000',
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
})
