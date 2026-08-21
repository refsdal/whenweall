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
export default defineConfig({
  testDir: 'e2e',
  timeout: 60_000,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: { baseURL: 'http://localhost:3000', trace: 'on-first-retry' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  // Applies local D1 migrations before the webServer/tests start — see e2e/global-setup.ts.
  globalSetup: './e2e/global-setup.ts',
  webServer: {
    command: 'bun run build && bunx vite preview --port 3000',
    url: 'http://localhost:3000',
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
})
