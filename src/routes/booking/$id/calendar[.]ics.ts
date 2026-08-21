import { createFileRoute } from '@tanstack/react-router'
import { env } from 'cloudflare:workers'
import { getAuth } from '#/server/auth/auth'
import { getDb } from '#/server/db/client'
import { errorCode } from '#/lib/errors'
import { buildIcs } from '#/lib/ics'
import { getBookingForManage } from '#/server/bookings/bookings'

/**
 * Extracted from the route's GET handler so it can be exercised directly in a workers test (401 /
 * 403 / 404 / 200) without booting the router — session lookup needs real `request.headers`, so
 * this can't reuse the `Route.options.server.handlers.GET` cast the poll `calendar.ics` test uses
 * (see `src/routes/p/$id/roster[.]csv.ts` for the same pattern with polls).
 *
 * Auth: a visitor's manage token (`?t=`) always works; without one, an owner session is required.
 * No token and no session is 401 (nothing to check auth *against* yet); a wrong token or a session
 * that doesn't own the booking's page is 403 (`getBookingForManage` throws `INVALID_TOKEN` /
 * `FORBIDDEN` for those); a booking that doesn't exist is 404 either way.
 */
export async function bookingIcsResponse(request: Request, bookingId: string): Promise<Response> {
  const token = new URL(request.url).searchParams.get('t')

  let auth: { token: string } | { ownerId: string }
  if (token) {
    auth = { token }
  } else {
    const session = await getAuth().api.getSession({ headers: request.headers })
    if (!session) return new Response('Unauthorized', { status: 401 })
    auth = { ownerId: session.user.id }
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
    uid: `${bookingId}@samla`,
    title: view.page.title,
    description: null,
    location: view.page.location,
    url: `${env.APP_URL}/booking/${bookingId}`,
    start: { dateTime: view.startAt, endDateTime: view.endAt },
  })

  return new Response(ics, {
    headers: {
      'Content-Type': 'text/calendar; charset=utf-8',
      'Content-Disposition': `attachment; filename="samla-booking-${bookingId}.ics"`,
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
