import path from 'node:path'
import { defineConfig } from 'vitest/config'
import { cloudflareTest, readD1Migrations } from '@cloudflare/vitest-plugin'

export default defineConfig(async () => {
  const migrations = await readD1Migrations(path.join(import.meta.dirname, 'drizzle'))
  return {
    test: {
      projects: [
        {
          test: {
            name: 'unit',
            // Distinct groupOrder is required because `workers` pins `maxWorkers` and this one
            // does not; it also means the jsdom project finishes before the workerd isolates
            // start, so the two pools never compete for cores.
            sequence: { groupOrder: 0 },
            environment: 'jsdom',
            include: [
              'src/**/*.test.{ts,tsx}',
              'emails/**/*.test.{ts,tsx}',
              'messages/**/*.test.ts',
            ],
            exclude: ['**/*.workers.test.ts'],
            setupFiles: ['./test/setup.unit.ts'],
          },
          resolve: { alias: { '#': path.resolve(import.meta.dirname, 'src') } },
        },
        {
          plugins: [
            cloudflareTest({
              wrangler: { configPath: './test/wrangler.test.jsonc' },
              miniflare: { bindings: { TEST_MIGRATIONS: migrations } },
            }),
          ],
          test: {
            name: 'workers',
            /**
             * The mail/auth/calendar modules are `await import()`ed at their send sites so that
             * React, @react-email and Better-Auth stay out of every isolate's bundle (see the
             * comments at those call sites). In workerd proper that import is a lazy instantiation
             * of an already-bundled module; under this pool it goes through Vite's *shared* module
             * runner at test runtime, and that runner does not scale with isolate count — past
             * ~4 concurrent isolates a single import can starve for 30s+ and fail the test, which
             * then keeps running and leaks its mail into the next test's assertions.
             *
             * Capping the pool costs nothing: measured on a 44-core machine the suite takes
             * ~2m30s at 4 workers and gets no faster unbounded, because the module runner — not
             * the CPU — is the bottleneck. CI runners have 4 cores anyway, so this only makes
             * big local machines behave like CI instead of failing on them.
             */
            maxWorkers: 4,
            sequence: { groupOrder: 1 },
            // Headroom for that one-off import in whichever test first reaches a send site: it is
            // a few seconds of Vite transform, well over the 5s default. Only the first test in
            // each file pays it — the module is cached in the isolate afterwards.
            testTimeout: 30_000,
            include: ['**/*.workers.test.ts'],
            // Unanchored so it also matches `test/**`; that also matches sibling agent
            // worktrees under `.claude/worktrees/**` (see `using-git-worktrees`) when one
            // happens to be checked out alongside this repo — those are separate checkouts,
            // sometimes on other branches, never this repo's own tests to run.
            exclude: ['.claude/**'],
            setupFiles: ['./test/apply-migrations.ts'],
          },
          resolve: { alias: { '#': path.resolve(import.meta.dirname, 'src') } },
        },
      ],
    },
  }
})
