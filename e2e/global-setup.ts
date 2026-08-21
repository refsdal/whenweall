import { execSync } from 'node:child_process'

/**
 * Applies pending D1 migrations to the local (miniflare-backed) database before any test runs.
 * Idempotent — `wrangler d1 migrations apply --local` is a no-op once the schema is current — so
 * this is safe to run on every `bun run test:e2e` invocation, not just a clean CI checkout.
 */
export default function globalSetup(): void {
  execSync('bun run db:migrate:local', { stdio: 'inherit' })
}
