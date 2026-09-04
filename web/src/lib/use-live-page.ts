import { useEffect, useRef, useState } from 'react'
import { connectRoom } from '#/lib/room-socket'

/** Local replacement for the old `#/do/booking-protocol` (a Cloudflare Durable Object wire type
 * that no longer exists). Booking pages have no presence UI (unlike polls), so this is
 * deliberately just the one event a live booking/manage page needs: "something about this page's
 * bookings changed, re-fetch availability/booking state." */
export type BookingRoomEvent = { type: 'page.changed' }

/**
 * Subscribes to a booking page's live room over `connectRoom` (`#/lib/room-socket`) —
 * `/api/v1/booking-pages/{pageId}/ws` per `internal/rooms/PROTOCOL.md`, which broadcasts no
 * presence for this route.
 *
 * Every booking, cancellation and reschedule on the page arrives as one `page.changed` event —
 * the caller usually answers it by invalidating the route. A fresh snapshot (first connect, every
 * reconnect, and every `resync`) forwards the same synthetic `page.changed` — the room's own
 * `PROTOCOL.md` rule that resync means "re-snapshot," not "trust `?since=`", applies here exactly
 * as it does for polls. The connection asks for no `?since=` backfill either: the snapshot is
 * ground truth and this hook refetches its full state on every one, so a per-frame backfill
 * refetch would be pure cost (PROTOCOL.md).
 *
 * `onEvent` is held in a ref so an inline arrow doesn't tear down the socket. No-ops during SSR.
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
    const room = connectRoom({
      path: `/api/v1/booking-pages/${pageId}/ws`,
      backfill: false,
      onSnapshot: () => {
        setConnected(true)
        onEventRef.current({ type: 'page.changed' })
      },
      onEvent: (type) => {
        if (type === 'page.changed') onEventRef.current({ type: 'page.changed' })
      },
      onResync: () => onEventRef.current({ type: 'page.changed' }),
    })

    return () => {
      setConnected(false)
      room.close()
    }
  }, [pageId])

  return { connected }
}
