import path from 'node:path'
import { defineConfig } from 'vitest/config'
import { cloudflareTest, readD1Migrations } from '@cloudflare/vitest-plugin'

/**
 * go-rewrite-08 task 1: the SPA and its `unit` (jsdom) vitest project moved to `web/` in full —
 * see `web/vitest.config.ts`. What's left here is only the `workers` project, which exercises
 * the backend-reference TS left behind at the repo root (`src/server`, `src/do`, `src/rooms`,
 * `src/routes/api`) against real D1/Durable Object bindings via `@cloudflare/vitest-plugin`.
 * That backend TS is reference material only until Task 8 deletes it for the Go rewrite — this
 * config exists so it stays runnable (`bun run test:workers`) until then, not because it's a
 * target of this migration.
 */
export default defineConfig(async () => {
  const migrations = await readD1Migrations(path.join(import.meta.dirname, 'drizzle'))
  return {
    plugins: [
      cloudflareTest({
        wrangler: { configPath: './test/wrangler.test.jsonc' },
        miniflare: { bindings: { TEST_MIGRATIONS: migrations } },
      }),
    ],
    test: {
      name: 'workers',
      // See the comment at this same option in the pre-split config (recovered via git history)
      // for why it's capped at 4: the module runner backing `await import()` for
      // mail/auth/calendar modules doesn't scale with isolate count past ~4 concurrent isolates.
      maxWorkers: 4,
      testTimeout: 30_000,
      include: ['**/*.workers.test.ts'],
      exclude: ['.claude/**', 'web/**', 'node_modules/**'],
      setupFiles: ['./test/apply-migrations.ts'],
    },
    resolve: { alias: { '#': path.resolve(import.meta.dirname, 'src') } },
  }
})
