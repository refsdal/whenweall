import { useEffect, useRef, useState } from 'react'
import { connectRoom } from '#/lib/room-socket'
import type { UsageStats } from '#/lib/stats-types'

function readStats(value: unknown): UsageStats | null {
  if (typeof value !== 'object' || value === null) return null
  const v = value as Record<string, unknown>
  const { pollsFinalized, pollsCreated, responsesYes, responsesIfNeedBe, responsesNo } = v
  if (
    typeof pollsFinalized !== 'number' ||
    typeof pollsCreated !== 'number' ||
    typeof responsesYes !== 'number' ||
    typeof responsesIfNeedBe !== 'number' ||
    typeof responsesNo !== 'number'
  ) {
    return null
  }
  return { pollsFinalized, pollsCreated, responsesYes, responsesIfNeedBe, responsesNo }
}

/**
 * Live usage counters for the landing page, over `/api/v1/stats/ws` (`internal/rooms/PROTOCOL.md`)
 * via `connectRoom`.
 *
 * Deliberately simpler than `useLivePoll`: the socket opens only once `enabled` turns true (the
 * caller gates that on the section scrolling into view). `connectRoom` itself still reconnects
 * with backoff on a genuine drop (the DO version's "no reconnect backoff" comment described the
 * *previous* raw-`WebSocket` implementation, which is gone now that this shares the same client
 * every other room uses) — a stale marketing counter recovering on its own is strictly better than
 * one that never does, and this route has nothing else competing for the retry budget.
 *
 * `stats:global` also has a REST read (`GET /api/v1/stats`, `web/src/api/stats.ts`) that the route
 * loader uses for first paint; this hook deliberately does not call it. The websocket's own
 * `snapshot` frame is this hook's source of a fresh read, both for an ordinary connect and for
 * `resync`: `internal/rooms/PROTOCOL.md`'s "on resync, refetch a snapshot" rule is satisfied here
 * by tearing down and reopening the socket, since reopening re-triggers that same snapshot frame.
 *
 * No-ops during SSR, and returns `initial` unchanged until a frame actually arrives, so the
 * server-rendered markup and the first client render always agree.
 */
export function useLiveStats(initial: UsageStats, enabled: boolean): UsageStats {
  const [stats, setStats] = useState(initial)

  // Held in a ref so a caller passing a fresh object literal each render doesn't reopen the socket.
  const initialRef = useRef(initial)
  useEffect(() => {
    initialRef.current = initial
  }, [initial])

  useEffect(() => {
    if (!enabled) return

    const applyStats = (data: unknown) => {
      const parsed = readStats(data)
      if (parsed) setStats(parsed)
    }

    let room: { close(): void }
    let disposed = false

    const open = () => {
      room = connectRoom({
        path: '/api/v1/stats/ws',
        onSnapshot: applyStats,
        onEvent: (type, data) => {
          if (type === 'stats') applyStats(data)
        },
        onResync: () => {
          // `stats:global` has no REST snapshot endpoint of its own — the websocket's own
          // `snapshot` frame IS this room's one source of a fresh read, so satisfying
          // PROTOCOL.md's "on resync, refetch a snapshot" rule here means reopening the socket,
          // which re-triggers that same snapshot frame.
          room.close()
          if (!disposed) open()
        },
      })
    }

    open()

    return () => {
      disposed = true
      room.close()
    }
  }, [enabled])

  return stats
}
