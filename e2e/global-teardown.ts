import { execSync } from 'node:child_process'
import { DB_CONTAINER, MAILPIT_CONTAINER } from './e2e-env'

/**
 * Stops and removes the throwaway containers `e2e/run-server.sh` started (as playwright.config.ts's
 * webServer.command — see that script's own header comment for why container lifecycle lives
 * there rather than in a separate `globalSetup` hook), and restores `internal/httpserver/dist` to
 * its committed placeholder state — the webServer command overwrites `index.html` (a tracked file)
 * with the real build's output for the run, and this undoes that so the working tree is clean
 * again once the suite finishes (pass or fail).
 */
export default function globalTeardown(): void {
  try {
    execSync(`docker rm -f ${DB_CONTAINER}`, { stdio: 'ignore' })
  } catch {
    // Already gone — nothing to clean up.
  }
  try {
    execSync(`docker rm -f ${MAILPIT_CONTAINER}`, { stdio: 'ignore' })
  } catch {
    // Already gone — nothing to clean up.
  }
  try {
    execSync('git clean -fdx -- internal/httpserver/dist', { stdio: 'ignore' })
    execSync('git checkout -- internal/httpserver/dist/index.html', { stdio: 'ignore' })
  } catch {
    // Best-effort: a dirty dist/ here is a cosmetic annoyance, never a reason to fail the run.
  }
}
