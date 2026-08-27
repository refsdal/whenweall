import { createFileRoute } from '@tanstack/react-router'
import { eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import { pollRoom } from '#/server/notifications/do-client'
import { enforceRateLimit } from '#/server/http/rate-limit'
import { pollIdSchema } from '#/server/polls/schemas'

export const Route = createFileRoute('/api/polls/$id/ws')({
  server: {
    handlers: {
      GET: async ({ request, params }) => {
        if (request.headers.get('Upgrade') !== 'websocket') {
          return new Response('Expected websocket', { status: 426 })
        }
        if (!pollIdSchema.safeParse(params.id).success) {
          return new Response('Bad id', { status: 400 })
        }

        // A route handler, not a server function, so it calls the limiter directly rather than
        // through `rateLimitMiddleware(...)` — that wrapper only composes into `createServerFn`
        // chains. Caught rather than propagated: `enforceRateLimit` throws `AppError`, which has
        // no error-mapping layer around it here and would surface as a 500.
        try {
          await enforceRateLimit('connect')
        } catch {
          return new Response('Too many requests', { status: 429 })
        }

        // `POLL_ROOM.getByName(id)` materialises a durable object for any well-formed id, so the
        // poll has to be known to exist *before* the stub is addressed — otherwise a script
        // generating random 12-character ids creates unbounded durable objects. `columns` keeps
        // this to an existence check: the poll row carries the title and description, and none of
        // that is wanted here.
        const poll = await getDb().query.polls.findFirst({
          where: eq(polls.id, params.id),
          columns: { id: true, deletedAt: true },
        })
        if (!poll || poll.deletedAt) {
          return new Response('Not found', { status: 404 })
        }

        return pollRoom(params.id).fetch(request)
      },
    },
  },
})
