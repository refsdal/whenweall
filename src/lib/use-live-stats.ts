import { useEffect, useRef, useState } from 'react'
import type { StatsEvent, UsageStats } from '#/do/stats-protocol'

function isStatsEvent(value: unknown): value is StatsEvent {
  if (typeof value !== 'object' || value === null) return false
  return (value as { type?: unknown }).type === 'stats'
}

/**
 * Live usage counters for the landing page.
 *
 * Deliberately simpler than `useLivePoll`: the socket opens only once `enabled` turns true (the
 * caller gates that on the section scrolling into view), and there is **no reconnect backoff**. A
 * stale marketing counter is not worth a retry loop on the busiest page in the product — if the
 * socket drops, the server-rendered numbers simply stay put until the next page load.
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
    if (typeof window === 'undefined' || typeof WebSocket === 'undefined') return

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const socket = new WebSocket(`${protocol}//${window.location.host}/api/stats/ws`)

    socket.addEventListener('message', (event) => {
      try {
        const parsed: unknown = JSON.parse(event.data as string)
        if (isStatsEvent(parsed)) setStats(parsed.stats)
      } catch {
        // A frame we can't parse is not worth tearing the socket down for.
      }
    })

    return () => {
      socket.close()
    }
  }, [enabled])

  return stats
}
