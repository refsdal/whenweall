import { api } from '#/api/client'
import { EMPTY_STATS, type UsageStats } from '#/lib/stats-types'

/**
 * The landing page's usage counters over REST (`GET /api/v1/stats`, `internal/rooms/endpoints.go`)
 * — the same object the stats websocket's snapshot frame carries under `data`
 * (`internal/rooms/PROTOCOL.md`). Used for first paint; `useLiveStats` takes over live.
 */
export function getUsageStats(opts?: { signal?: AbortSignal }): Promise<UsageStats> {
  return api<UsageStats>('GET', '/api/v1/stats', undefined, opts)
}

/**
 * The loader awaits this read, so a struggling backend must not be allowed to stall first paint
 * indefinitely — the marketing page rendered instantly from `EMPTY_STATS` before this loader
 * existed, and a slow `/api/v1/stats` (e.g. Postgres under load) must degrade back to that, not
 * hang. 2s is generous for a same-origin JSON GET but short enough nobody notices the landing page
 * "loading".
 */
const LOAD_TIMEOUT_MS = 2000

/**
 * Route loader for `/`: real numbers before the first render, so the section never flashes 0/0/0
 * and is correct even where a reverse proxy drops WebSocket upgrades. A failed OR slow read must
 * never take the landing page down with it — it degrades to zeros (immediately on error, within
 * `timeoutMs` on a hang) and the socket fills them in later.
 *
 * `timeoutMs` defaults to `LOAD_TIMEOUT_MS` and exists as a parameter only so tests can exercise
 * the hang path without an actual multi-second wait — the route itself (`routes/index.tsx`) always
 * calls this with no argument.
 */
export async function loadLandingStats(timeoutMs = LOAD_TIMEOUT_MS): Promise<{ stats: UsageStats }> {
  try {
    return { stats: await getUsageStats({ signal: AbortSignal.timeout(timeoutMs) }) }
  } catch {
    return { stats: EMPTY_STATS }
  }
}
