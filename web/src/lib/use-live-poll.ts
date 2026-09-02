import { useEffect, useRef, useState } from 'react'
import type { PollEvent } from '#/do/protocol'

const FIRST_DELAY_MS = 1000
const MAX_DELAY_MS = 30_000

function isPollEvent(value: unknown): value is PollEvent {
  if (typeof value !== 'object' || value === null) return false
  const type = (value as { type?: unknown }).type
  return type === 'poll.changed' || type === 'presence'
}

/**
 * Subscribes to a poll's `PollRoom` durable object over a websocket.
 *
 * The socket is the page's live wire: every vote, comment and settings change arrives as a
 * `poll.changed` event (the caller usually answers it by invalidating the route), and the room
 * reports how many people are looking at the poll right now.
 *
 * Reconnects with a 1s → 30s exponential backoff, resetting once a connection actually opens, so
 * a laptop waking from sleep reconnects quickly while a downed server isn't hammered. No-ops
 * during SSR. `onEvent` is held in a ref so a caller can pass an inline arrow function without
 * tearing down the socket on every render.
 */
export function useLivePoll(
  pollId: string,
  onEvent: (event: PollEvent) => void,
): { connected: boolean; presence: number } {
  const [connected, setConnected] = useState(false)
  const [presence, setPresence] = useState(0)

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
      const ws = new WebSocket(`${protocol}://${window.location.host}/api/polls/${pollId}/ws`)
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
        if (!isPollEvent(parsed)) return
        if (parsed.type === 'presence') setPresence(parsed.count)
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
  }, [pollId])

  return { connected, presence }
}
