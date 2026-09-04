import path from 'node:path'
import { defineConfig } from 'vitest/config'

/**
 * The `workers` project (Cloudflare Workers/D1 integration tests, via `@cloudflare/vitest-plugin`)
 * stayed behind at the repo root with the rest of the backend-reference TS — it needs
 * `@react-email`/Better-Auth/Drizzle/D1, none of which are `web/` dependencies anymore. Some
 * `*.workers.test.ts` files still physically live under `web/src` (moved along with the frontend
 * component they exercise, e.g. `components/creator/__tests__/creator-state.workers.test.ts`,
 * which asserts a reducer against the real backend service) — they're excluded here rather than
 * deleted, and will fail typecheck (see worklist.txt) until Tasks 2-4 give them a Go-backed
 * equivalent or a mock.
 */
export default defineConfig({
  test: {
    name: 'unit',
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}', 'messages/**/*.test.ts'],
    exclude: ['**/*.workers.test.ts', 'node_modules/**'],
    setupFiles: ['./test/setup.unit.ts'],
  },
  resolve: { alias: { '#': path.resolve(import.meta.dirname, 'src') } },
})
