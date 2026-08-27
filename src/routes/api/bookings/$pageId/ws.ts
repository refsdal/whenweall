import { createFileRoute } from '@tanstack/react-router'
import { eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { bookingPages } from '#/server/db/schema'
import { bookingRoom } from '#/server/notifications/booking-client'
import { enforceRateLimit } from '#/server/http/rate-limit'
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

        try {
          await enforceRateLimit('connect')
        } catch {
          return new Response('Too many requests', { status: 429 })
        }

        // As on `/api/polls/$id/ws`: `getByName` materialises a durable object for any
        // well-formed id, so existence is checked before the stub is addressed. `BookingRoom.
        // fetch` writes no storage, so an unknown id here is cheaper than it was for `PollRoom` —
        // but it still costs a durable object instance and the duration of every held connection.
        const page = await getDb().query.bookingPages.findFirst({
          where: eq(bookingPages.id, params.pageId),
          columns: { id: true, deletedAt: true },
        })
        if (!page || page.deletedAt) {
          return new Response('Not found', { status: 404 })
        }

        return bookingRoom(params.pageId).fetch(request)
      },
    },
  },
})
