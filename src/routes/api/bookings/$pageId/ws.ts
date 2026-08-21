import { createFileRoute } from '@tanstack/react-router'
import { bookingRoom } from '#/server/notifications/booking-client'
import { pageIdSchema } from '#/server/bookings/schemas'

export const Route = createFileRoute('/api/bookings/$pageId/ws')({
  server: {
    handlers: {
      GET: async ({ request, params }) => {
        if (request.headers.get('Upgrade') !== 'websocket') {
          return new Response('Expected websocket', { status: 426 })
        }
        // Same pre-database sanity check as `/api/polls/$id/ws`'s `pollIdSchema`, sized to this
        // route's own id shape (see `pageIdSchema`'s doc comment).
        if (!pageIdSchema.safeParse(params.pageId).success) {
          return new Response('Bad id', { status: 400 })
        }

        return bookingRoom(params.pageId).fetch(request)
      },
    },
  },
})
