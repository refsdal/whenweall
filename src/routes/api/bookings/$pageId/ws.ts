import { createFileRoute } from '@tanstack/react-router'
import { bookingRoom } from '#/server/notifications/booking-client'

/** Loose sanity check on a page id before routing to a Durable Object — `newId()` (nanoid, 16
 * chars from the URL-safe alphabet) is what booking pages are actually keyed by, but this is kept
 * deliberately permissive rather than pinned to that exact length. */
const PAGE_ID_RE = /^[A-Za-z0-9_-]{1,64}$/

export const Route = createFileRoute('/api/bookings/$pageId/ws')({
  server: {
    handlers: {
      GET: async ({ request, params }) => {
        if (request.headers.get('Upgrade') !== 'websocket') {
          return new Response('Expected websocket', { status: 426 })
        }
        if (!PAGE_ID_RE.test(params.pageId)) {
          return new Response('Bad id', { status: 400 })
        }

        return bookingRoom(params.pageId).fetch(request)
      },
    },
  },
})
