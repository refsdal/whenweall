import { execSync } from 'node:child_process'
import { existsSync, readFileSync, rmSync } from 'node:fs'
import { RUN_MARKER } from './e2e-env'

/** What e2e/run-server.sh records about the things IT started for this run. */
type RunMarker = { mode: 'go'; containers: string[]; dist: boolean }

/**
 * Cleans up exactly what `e2e/run-server.sh` recorded in its marker file for this run — the
 * throwaway containers it created, and the `internal/httpserver/dist` placeholder it overwrote
 * with the real build — then deletes the marker.
 *
 * No marker means this run started nothing: either Playwright reused a server a developer is
 * keeping alive (`reuseExistingServer`), or the suite ran in image mode against the compose stack
 * (`E2E_SERVER=image`, brought down by `e2e/compose-e2e.sh down -v`). In both cases tearing down
 * containers by name would destroy something that isn't ours, so this does nothing at all.
 */
export default function globalTeardown(): void {
  if (!existsSync(RUN_MARKER)) return

  let marker: RunMarker
  try {
    marker = JSON.parse(readFileSync(RUN_MARKER, 'utf8')) as RunMarker
  } catch {
    // An unreadable marker is not worth failing a finished run over — drop it and move on.
    rmSync(RUN_MARKER, { force: true })
    return
  }

  for (const name of marker.containers ?? []) {
    try {
      execSync(`docker rm -f ${name}`, { stdio: 'ignore' })
    } catch {
      // Already gone — nothing to clean up.
    }
  }

  if (marker.dist) {
    try {
      execSync('git clean -fdx -- internal/httpserver/dist', { stdio: 'ignore' })
      execSync('git checkout -- internal/httpserver/dist/index.html', { stdio: 'ignore' })
    } catch {
      // Best-effort: a dirty dist/ here is a cosmetic annoyance, never a reason to fail the run.
    }
  }

  rmSync(RUN_MARKER, { force: true })
}
