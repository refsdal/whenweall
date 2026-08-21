import { useEffect, useRef, useState } from 'react'
import type { BookingRoomEvent } from '#/do/booking-protocol'

const FIRST_DELAY_MS = 1000
const MAX_DELAY_MS = 30_000

function isPageEvent(value: unknown): value is BookingRoomEvent {
  if (typeof value !== 'object' || value === null) return false
  return (value as { type?: unknown }).type === 'page.changed'
}

/**
 * Subscribes to a booking page's `BookingRoom` durable object over a websocket.
 *
 * Every booking, cancellation and reschedule on the page arrives as one `page.changed` event —
 * the caller usually answers it by invalidating the route, so a slot someone else just took
 * disappears from the list while you are still looking at it.
 *
 * Same shape as `useLivePoll` (1s → 30s backoff, reset on open, no-op during SSR, `onEvent` held
 * in a ref so an inline arrow doesn't tear down the socket); booking rooms broadcast no presence,
 * so there is no count to report.
 */
export function useLivePage(
  pageId: string,
  onEvent: (event: BookingRoomEvent) => void,
): { connected: boolean } {
  const [connected, setConnected] = useState(false)

  const onEventRef = useRef(onEvent)
  useEffect(() => {
    onEventRef.current = onEvent
  }, [onEvent])

  useEffect(() => {
    if (typeof window === 'undefined' || typeof WebSocket === 'undefined') return

    let disposed = false
    let socket: WebSocket | null = null
    let retry: ReturnType<typeof setTimeout> | undefined
    let delay = FIRST_DELAY_MS

    const connect = () => {
      const protocol = window.location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(`${protocol}://${window.location.host}/api/bookings/${pageId}/ws`)
      socket = ws

      ws.onopen = () => {
        delay = FIRST_DELAY_MS
        setConnected(true)
      }

      ws.onmessage = (event: MessageEvent) => {
        if (typeof event.data !== 'string') return
        let parsed: unknown
        try {
          parsed = JSON.parse(event.data)
        } catch {
          return // Keep-alive frames ("pong") and anything else non-JSON.
        }
        if (!isPageEvent(parsed)) return
        onEventRef.current(parsed)
      }

      ws.onerror = () => {
        try {
          ws.close()
        } catch {
          // Already closing; `onclose` still schedules the retry.
        }
      }

      ws.onclose = () => {
        setConnected(false)
        if (disposed) return
        retry = setTimeout(connect, delay)
        delay = Math.min(delay * 2, MAX_DELAY_MS)
      }
    }

    connect()

    return () => {
      disposed = true
      if (retry !== undefined) clearTimeout(retry)
      try {
        socket?.close()
      } catch {
        // Nothing to clean up if the socket never opened.
      }
    }
  }, [pageId])

  return { connected }
}
