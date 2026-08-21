import { createFileRoute } from '@tanstack/react-router'
import { eq } from 'drizzle-orm'
import { getAuth } from '#/server/auth/auth'
import { getDb } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import { getLocale } from '#/paraglide/runtime'
import { buildRosterCsv } from '#/server/polls/roster'
import { pollIdSchema } from '#/server/polls/schemas'

/**
 * Extracted from the route's GET handler so it can be exercised directly in a workers test (400 /
 * 403 / 404 / 200) without booting the router — session lookup needs real `request.headers`, so
 * this can't reuse the `Route.options.server.handlers.GET` cast the calendar.ics test uses.
 */
export async function rosterResponse(request: Request, id: string): Promise<Response> {
  if (!pollIdSchema.safeParse(id).success) {
    return new Response('Bad id', { status: 400 })
  }

  const session = await getAuth().api.getSession({ headers: request.headers })

  const poll = await getDb().query.polls.findFirst({
    where: eq(polls.id, id),
    columns: { ownerId: true, deletedAt: true, type: true },
  })
  if (!poll || poll.deletedAt) return new Response('Not found', { status: 404 })
  if (!session || session.user.id !== poll.ownerId) {
    return new Response('Forbidden', { status: 403 })
  }
  // Owner-only past this point, so there's no information to leak either way — a plain 400 is
  // simpler than pretending the route doesn't exist for a poll type that just has no roster.
  if (poll.type !== 'signup') {
    return new Response('Not a sign-up sheet', { status: 400 })
  }

  const csv = await buildRosterCsv(getDb(), id, { locale: getLocale() })

  return new Response(csv, {
    headers: {
      'Content-Type': 'text/csv; charset=utf-8',
      'Content-Disposition': `attachment; filename="samla-${id}-roster.csv"`,
    },
  })
}

export const Route = createFileRoute('/p/$id/roster.csv')({
  server: {
    handlers: {
      GET: ({ request, params }) => rosterResponse(request, params.id),
    },
  },
})
