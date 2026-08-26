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
