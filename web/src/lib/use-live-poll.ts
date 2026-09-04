import { useEffect, useRef, useState } from 'react'
import { connectRoom } from '#/lib/room-socket'

/** Local replacement for the old `#/do/protocol` (a Cloudflare Durable Object wire type that no
 * longer exists) — the two events a poll page ever needs to react to, unchanged in name and shape
 * from that file: `internal/rooms`'s Go hub keeps this same vocabulary (PROTOCOL.md), just
 * delivered as a flattened `room_events` frame instead of a DO message. */
export type PollEvent =
  | { type: 'poll.changed'; entity: 'poll' | 'participant' | 'vote' | 'comment' }
  | { type: 'presence'; count: number }

type PollChangedEntity = Extract<PollEvent, { type: 'poll.changed' }>['entity']

function isEntity(value: unknown): value is PollChangedEntity {
  return value === 'poll' || value === 'participant' || value === 'vote' || value === 'comment'
}

/**
 * Subscribes to a poll's live room over `connectRoom` (`#/lib/room-socket`, the plan-8 wire
 * protocol — see `internal/rooms/PROTOCOL.md`).
 *
 * Every vote, comment and settings change arrives as a `poll.changed` event (the caller usually
 * answers it by invalidating the route), and the room reports how many people are looking at the
 * poll right now. A fresh snapshot (first connect, every reconnect, and every `resync`) is treated
 * the same as a `poll.changed`: `onSnapshot`'s own data is a full `PollView` this hook doesn't need
 * to hold onto (the route's REST loader already owns that state), so it forwards a synthetic
 * `poll.changed` to `onEvent` for it — same "go re-fetch" effect the DO version got for free from
 * `connected` flipping true, now covering `resync` too (PROTOCOL.md's own rule: resync means
 * "re-snapshot," not "trust `?since=`"). The connection carries no identity and asks for no
 * `?since=` backfill: the snapshot is ground truth and the route refetches over REST (with the
 * guest token in a header) on every snapshot, so a token on the URL — which proxies log — and
 * per-frame backfill refetches would both be pure cost (PROTOCOL.md).
 *
 * `onEvent` is held in a ref so a caller can pass an inline arrow function without tearing down the
 * socket on every render. No-ops during SSR.
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
    const room = connectRoom({
      path: `/api/v1/polls/${pollId}/ws`,
      backfill: false,
      onSnapshot: () => {
        setConnected(true)
        onEventRef.current({ type: 'poll.changed', entity: 'poll' })
      },
      onEvent: (type, data) => {
        if (type === 'presence') {
          const count = (data as { count?: unknown }).count
          if (typeof count === 'number') setPresence(count)
          return
        }
        if (type === 'poll.changed') {
          const entity = (data as { entity?: unknown }).entity
          if (isEntity(entity)) onEventRef.current({ type: 'poll.changed', entity })
        }
      },
      onResync: () => onEventRef.current({ type: 'poll.changed', entity: 'poll' }),
    })

    return () => {
      setConnected(false)
      room.close()
    }
  }, [pollId])

  return { connected, presence }
}
