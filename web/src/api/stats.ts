import { api } from '#/api/client'
import { EMPTY_STATS, type UsageStats } from '#/lib/stats-types'

/**
 * The landing page's usage counters over REST (`GET /api/v1/stats`, `internal/rooms/endpoints.go`)
 * — the same object the stats websocket's snapshot frame carries under `data`
 * (`internal/rooms/PROTOCOL.md`). Used for first paint; `useLiveStats` takes over live.
 */
export function getUsageStats(): Promise<UsageStats> {
  return api<UsageStats>('GET', '/api/v1/stats')
}

/**
 * Route loader for `/`: real numbers before the first render, so the section never flashes 0/0/0
 * and is correct even where a reverse proxy drops WebSocket upgrades. A failed read must never
 * take the landing page down with it — it degrades to zeros and the socket fills them in later.
 */
export async function loadLandingStats(): Promise<{ stats: UsageStats }> {
  try {
    return { stats: await getUsageStats() }
  } catch {
    return { stats: EMPTY_STATS }
  }
}
