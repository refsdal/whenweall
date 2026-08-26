import { createServerFn } from '@tanstack/react-start'
import { env } from 'cloudflare:workers'
import { and, eq } from 'drizzle-orm'
import * as z from 'zod'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import {
  bookingPages,
  bookings,
  member,
  type BookingPage,
  type CancelledBy,
} from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { generateSlots, type Interval } from '#/lib/availability'
import { getLocale } from '#/paraglide/runtime'
import { sessionMiddleware } from '#/server/auth/middleware'
import { requireOrgMiddleware, type OrgRole } from '#/server/auth/org'
import { rateLimitMiddleware } from '#/server/http/rate-limit.middleware'
import { requireTurnstile } from '#/server/http/turnstile'
import { createCalendarClient, getGoogleAccessToken } from '#/server/google/calendar'
import {
  bookViaRoom,
  cancelViaRoom,
  rescheduleViaRoom,
} from '#/server/notifications/booking-client'
import { sendBookingEmails } from './emails'
import {
  syncGoogleEventCreate,
  syncGoogleEventDelete,
  syncGoogleEventsForReschedule,
} from './google-sync'
import { getPublicPage } from './pages'
import * as bookingService from './bookings'
import type { ActingOrg } from './bookings'
import {
  bookSlotSchema,
  manageBookingSchema,
  publicAvailabilityQuerySchema,
  rescheduleSchema,
} from './schemas'

/*
 * A `createServerFn(...)` object doesn't expose its `.middleware([...])` array at runtime — see
 * the same note in `pages.functions.ts`/`polls.functions.ts`. These arrays are declared once here
 * and reused both to build each function below and as the manifest that test asserts against.
 */
const BOOK_LIMIT_ONLY = [rateLimitMiddleware('book')] as const
const SESSION_ONLY = [sessionMiddleware] as const
const SESSION_AND_BOOK_LIMIT = [sessionMiddleware, rateLimitMiddleware('book')] as const
const REQUIRE_ORG = [requireOrgMiddleware] as const

export const SERVER_FN_MIDDLEWARE = {
  getPublicAvailability: BOOK_LIMIT_ONLY,
  bookSlot: SESSION_AND_BOOK_LIMIT,
  // A visitor's manage token always works without a session at all; the owner path needs a
  // session but not necessarily *this* org — `manageAuthFor` falls back to a plain session id,
  // and `getBookingForManage`/`cancelBooking`/`rescheduleBooking` do their own org auth once
  // they've loaded the booking's page (its org, not the caller's active one, is what matters).
  getManagedBooking: SESSION_ONLY,
  cancelBooking: SESSION_AND_BOOK_LIMIT,
  rescheduleBooking: SESSION_AND_BOOK_LIMIT,
  listPageBookings: REQUIRE_ORG,
} as const

/**
 * Busy intervals for a booking-page's slot generation/validation: the page's own confirmed
 * bookings, plus — when the page has Google Calendar sync on and its `memberUserId` (whose
 * calendar the page reads/writes) still has a usable token — their Google freebusy over the same
 * range. A freebusy failure degrades to "no extra busy intervals" rather than failing the caller;
 * Google sync is always optional (spec decision 3).
 */
async function computeBusy(
  db: Db,
  page: BookingPage,
  range: { from: Date; to: Date },
): Promise<Interval[]> {
  const busy = await bookingService.bookedIntervals(db, page.id, range)
  if (!page.googleSync || !page.memberUserId) return busy

  try {
    const token = await getGoogleAccessToken(page.memberUserId)
    if (!token) return busy
    const google = await createCalendarClient().getFreeBusy(token, {
      timeMin: range.from.toISOString(),
      timeMax: range.to.toISOString(),
    })
    return [...busy, ...google]
  } catch (err) {
    console.error('[bookings.functions] Google freebusy lookup failed', err)
    return busy
  }
}

async function requireDbPage(db: Db, pageId: string): Promise<BookingPage> {
  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
  if (!page || page.deletedAt) throw new AppError('NOT_FOUND')
  return page
}

/**
 * Resolves the signed-in owner's org auth for `getManagedBooking`/`cancelBooking`/
 * `rescheduleBooking`'s no-token path — the same "active org + membership role" `requireOrgMiddleware`
 * derives, duplicated here because those three server fns also serve a visitor's manage-token path
 * with no session at all, so they can't unconditionally require one via middleware.
 */
async function requireOwnerAuth(
  session: { user: { id: string }; session: unknown } | null | undefined,
): Promise<{ org: ActingOrg; userId: string }> {
  if (!session) throw new AppError('UNAUTHORIZED')
  const activeOrgId = (session.session as { activeOrganizationId?: string | null })
    .activeOrganizationId
  if (!activeOrgId) throw new AppError('UNAUTHORIZED')
  const membership = await getDb().query.member.findFirst({
    where: and(eq(member.organizationId, activeOrgId), eq(member.userId, session.user.id)),
  })
  if (!membership) throw new AppError('FORBIDDEN')
  return { org: { id: activeOrgId, role: membership.role as OrgRole }, userId: session.user.id }
}

export const getPublicAvailability = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getPublicAvailability)
  .validator(publicAvailabilityQuerySchema)
  .handler(async ({ data }) => {
    const db = getDb()
    const page = await getPublicPage(db, data.handle, data.slug)
    if (!page) return null

    const dbPage = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, page.id) })
    // Race with a concurrent delete between the two lookups above — treat it the same as "no such
    // page" rather than crashing.
    if (!dbPage) return null

    const from = new Date(`${data.from}T00:00:00.000Z`)
    const to = new Date(`${data.to}T23:59:59.999Z`)
    const now = new Date()

    const busy = await computeBusy(db, dbPage, { from, to })
    const slots = generateSlots(bookingService.pageRulesFrom(dbPage), { from, to, now, busy })

    return { page, slots }
  })

export const bookSlot = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.bookSlot)
  .validator(bookSlotSchema)
  .handler(async ({ data }) => {
    await requireTurnstile(data.turnstileToken)

    const db = getDb()
    const page = await requireDbPage(db, data.pageId)

    const startMs = new Date(data.startAt).getTime()
    const range = { from: new Date(startMs - 86_400_000), to: new Date(startMs + 86_400_000) }
    const busy = await computeBusy(db, page, range)

    const result = await bookViaRoom(
      data.pageId,
      {
        startAt: data.startAt,
        name: data.name,
        email: data.email,
        note: data.note ?? null,
        locale: getLocale(),
        timezone: data.timezone,
      },
      busy,
    )

    const endAt = new Date(startMs + page.slotDurationMin * 60_000).toISOString()
    await syncGoogleEventCreate(env, db, page, result.bookingId, {
      startAt: data.startAt,
      endAt,
      attendeeEmail: data.email,
    })

    await sendBookingEmails(env, 'confirmed', result.bookingId, {
      db,
      manageToken: result.manageToken,
    })

    return result
  })

/** Resolves who's allowed to see/manage one booking: the visitor's manage token, or the signed-in
 * owner's active-org auth (`requireOwnerAuth` — checked inside
 * `getBookingForManage`/`cancelBooking`/`rescheduleBooking` themselves). */
async function manageAuthFor(
  token: string | undefined,
  session: { user: { id: string }; session: unknown } | null | undefined,
): Promise<{ token: string } | { org: ActingOrg; userId: string }> {
  if (token) return { token }
  return requireOwnerAuth(session)
}

export const getManagedBooking = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getManagedBooking)
  .validator(manageBookingSchema)
  .handler(async ({ data, context }) => {
    const auth = await manageAuthFor(data.token, context.session)
    return bookingService.getBookingForManage(getDb(), data.bookingId, auth)
  })

export const cancelBooking = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.cancelBooking)
  .validator(manageBookingSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    const auth = await manageAuthFor(data.token, context.session)
    const view = await bookingService.getBookingForManage(db, data.bookingId, auth)
    const by: CancelledBy = data.token ? 'visitor' : 'organiser'

    const result = await cancelViaRoom(view.page.id, data.bookingId, by)

    if (result.changed) {
      const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, data.bookingId) })
      if (booking?.googleEventId) {
        const page = await db.query.bookingPages.findFirst({
          where: eq(bookingPages.id, view.page.id),
        })
        // Delete unconditionally when there's a known event id — not gated on the page's current
        // `googleSync` toggle (finding 7): the event was created while sync was on, so it still
        // needs cleaning up even if sync has since been turned off.
        if (page) {
          await syncGoogleEventDelete(env, db, page, data.bookingId, booking.googleEventId)
        }
      }

      await sendBookingEmails(env, 'cancelled', data.bookingId, { db })
    }

    return result
  })

export const rescheduleBooking = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.rescheduleBooking)
  .validator(rescheduleSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    const auth = await manageAuthFor(data.token, context.session)
    const view = await bookingService.getBookingForManage(db, data.bookingId, auth)

    const page = await requireDbPage(db, view.page.id)
    const startMs = new Date(data.startAt).getTime()
    const range = { from: new Date(startMs - 86_400_000), to: new Date(startMs + 86_400_000) }
    const busy = await computeBusy(db, page, range)

    const result = await rescheduleViaRoom(page.id, data.bookingId, data.startAt, busy)

    const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, data.bookingId) })
    if (booking) {
      // Always delete a known event, regardless of the page's current `googleSync` toggle — same
      // reasoning as `cancelBooking` above (finding 7). The *create* of the new event is gated on
      // `googleSync` *and* on the delete having actually succeeded: creating a replacement event
      // after a failed delete would leave two live Google events for one booking, with
      // `googleEventId` overwritten to point at the new one and the old one orphaned on the
      // organiser's calendar — see `syncGoogleEventsForReschedule`.
      const endAt = new Date(startMs + page.slotDurationMin * 60_000).toISOString()
      await syncGoogleEventsForReschedule(env, db, page, data.bookingId, booking.googleEventId, {
        startAt: data.startAt,
        endAt,
        attendeeEmail: booking.visitorEmail,
      })
    }

    // Reschedules initiated by the visitor still have their manage token in hand (`data.token`) —
    // pass it along so the confirmation email's manage link keeps working. An owner-initiated
    // reschedule has no plaintext token to hand back (only its hash is stored), so the email falls
    // back to the bare booking URL (see `sendBookingEmails`'s doc comment).
    await sendBookingEmails(env, 'rescheduled', data.bookingId, {
      db,
      manageToken: data.token,
      previousStartAt: result.previousStartAt,
    })

    return result
  })

export const listPageBookings = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.listPageBookings)
  .validator(z.object({ pageId: z.string(), from: z.iso.datetime(), to: z.iso.datetime() }))
  .handler(async ({ data, context }) => {
    return bookingService.listBookings(getDb(), data.pageId, context.org, context.session.user.id, {
      from: new Date(data.from),
      to: new Date(data.to),
    })
  })
