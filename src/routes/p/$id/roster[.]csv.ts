import { createFileRoute } from '@tanstack/react-router'
import { and, eq } from 'drizzle-orm'
import { getAuth } from '#/server/auth/auth'
import { getDb } from '#/server/db/client'
import { member, polls } from '#/server/db/schema'
import { getLocale } from '#/paraglide/runtime'
import { canManageContent, type OrgRole } from '#/server/auth/org-roles'
import { buildRosterCsv } from '#/server/polls/roster'
import { pollIdSchema } from '#/server/polls/schemas'

/**
 * Extracted from the route's GET handler so it can be exercised directly in a workers test (400 /
 * 401 / 403 / 404 / 200) without booting the router — session lookup needs real `request.headers`,
 * so this can't reuse the `Route.options.server.handlers.GET` cast the calendar.ics test uses.
 *
 * Auth: NOT_FOUND-shaped 404 for a missing poll *or* a poll belonging to an org other than the
 * caller's active one (no leaking whether a poll id exists outside the caller's own org);
 * FORBIDDEN-shaped 403 for the right org but a role that can't manage this poll (a plain member
 * viewing someone else's roster); 401 with no session at all.
 */
export async function rosterResponse(request: Request, id: string): Promise<Response> {
  if (!pollIdSchema.safeParse(id).success) {
    return new Response('Bad id', { status: 400 })
  }

  const poll = await getDb().query.polls.findFirst({
    where: eq(polls.id, id),
    columns: { organizationId: true, createdBy: true, deletedAt: true, type: true },
  })
  if (!poll || poll.deletedAt) return new Response('Not found', { status: 404 })

  const session = await getAuth().api.getSession({ headers: request.headers })
  if (!session) return new Response('Unauthorized', { status: 401 })

  const activeOrgId = (session.session as { activeOrganizationId?: string | null })
    .activeOrganizationId
  if (!activeOrgId || activeOrgId !== poll.organizationId) {
    return new Response('Not found', { status: 404 })
  }

  const membership = await getDb().query.member.findFirst({
    where: and(eq(member.organizationId, activeOrgId), eq(member.userId, session.user.id)),
  })
  if (!membership) return new Response('Not found', { status: 404 })
  if (!canManageContent({ role: membership.role as OrgRole }, session.user.id, poll.createdBy)) {
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
      'Content-Disposition': `attachment; filename="whenweall-${id}-roster.csv"`,
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
