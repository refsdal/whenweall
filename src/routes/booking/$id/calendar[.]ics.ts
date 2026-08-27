import { createFileRoute } from '@tanstack/react-router'
import { env } from 'cloudflare:workers'
import { and, eq } from 'drizzle-orm'
import { getAuth } from '#/server/auth/auth'
import { getDb } from '#/server/db/client'
import { member } from '#/server/db/schema'
import { errorCode } from '#/lib/errors'
import { buildIcs } from '#/lib/ics'
import { getBookingForManage, type ActingOrg } from '#/server/bookings/bookings'
import { type OrgRole } from '#/server/auth/org-roles'

/**
 * Extracted from the route's GET handler so it can be exercised directly in a workers test (401 /
 * 403 / 404 / 200) without booting the router — session lookup needs real `request.headers`, so
 * this can't reuse the `Route.options.server.handlers.GET` cast the poll `calendar.ics` test uses
 * (see `src/routes/p/$id/roster[.]csv.ts` for the same pattern with polls).
 *
 * Auth: a visitor's manage token (`?t=`) always works; without one, an owner session with an
 * active org is required. No token and no usable org is 401 (nothing to check auth *against*
 * yet); a booking that doesn't exist, or whose page belongs to a different org than the caller's
 * active one, is 404 (`getBookingForManage`'s `NOT_FOUND` — no leaking whether a booking id
 * exists outside the caller's own org); a wrong token, or the right org but a role that can't
 * manage the page, is 403 (`INVALID_TOKEN`/`FORBIDDEN`).
 */
export async function bookingIcsResponse(request: Request, bookingId: string): Promise<Response> {
  const token = new URL(request.url).searchParams.get('t')

  let auth: { token: string } | { org: ActingOrg; userId: string }
  if (token) {
    auth = { token }
  } else {
    const session = await getAuth().api.getSession({ headers: request.headers })
    if (!session) return new Response('Unauthorized', { status: 401 })
    const activeOrgId = (session.session as { activeOrganizationId?: string | null })
      .activeOrganizationId
    if (!activeOrgId) return new Response('Unauthorized', { status: 401 })
    const membership = await getDb().query.member.findFirst({
      where: and(eq(member.organizationId, activeOrgId), eq(member.userId, session.user.id)),
    })
    if (!membership) return new Response('Forbidden', { status: 403 })
    auth = {
      org: { id: activeOrgId, role: membership.role as OrgRole },
      userId: session.user.id,
    }
  }

  let view: Awaited<ReturnType<typeof getBookingForManage>>
  try {
    view = await getBookingForManage(getDb(), bookingId, auth)
  } catch (err) {
    const code = errorCode(err)
    if (code === 'NOT_FOUND') return new Response('Not found', { status: 404 })
    if (code === 'INVALID_TOKEN' || code === 'FORBIDDEN') {
      return new Response('Forbidden', { status: 403 })
    }
    throw err
  }

  const ics = buildIcs({
    uid: `${bookingId}@whenweall`,
    title: view.page.title,
    description: null,
    location: view.page.location,
    url: `${env.APP_URL}/booking/${bookingId}`,
    start: { dateTime: view.startAt, endDateTime: view.endAt },
  })

  return new Response(ics, {
    headers: {
      'Content-Type': 'text/calendar; charset=utf-8',
      'Content-Disposition': `attachment; filename="whenweall-booking-${bookingId}.ics"`,
    },
  })
}

export const Route = createFileRoute('/booking/$id/calendar.ics')({
  server: {
    handlers: {
      GET: ({ request, params }) => bookingIcsResponse(request, params.id),
    },
  },
})
