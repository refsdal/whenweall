import { createServerFn } from '@tanstack/react-start'
import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import * as z from 'zod'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import { bookingPages, bookings, type BookingPage, type CancelledBy } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { generateSlots, type Interval } from '#/lib/availability'
import { getLocale } from '#/paraglide/runtime'
import { requireSessionMiddleware, sessionMiddleware } from '#/server/auth/middleware'
import { rateLimitMiddleware } from '#/server/http/rate-limit.middleware'
import { requireTurnstile } from '#/server/http/turnstile'
import { createCalendarClient, getGoogleAccessToken } from '#/server/google/calendar'
import {
  bookViaRoom,
  cancelViaRoom,
  rescheduleViaRoom,
} from '#/server/notifications/booking-client'
import { sendBookingEmails } from './emails'
import { getPublicPage } from './pages'
import * as bookingService from './bookings'
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
const REQUIRE_SESSION = [requireSessionMiddleware] as const

export const SERVER_FN_MIDDLEWARE = {
  getPublicAvailability: BOOK_LIMIT_ONLY,
  bookSlot: SESSION_AND_BOOK_LIMIT,
  getManagedBooking: SESSION_ONLY,
  cancelBooking: SESSION_AND_BOOK_LIMIT,
  rescheduleBooking: SESSION_AND_BOOK_LIMIT,
  listPageBookings: REQUIRE_SESSION,
} as const

/**
 * Busy intervals for a booking-page's slot generation/validation: the page's own confirmed
 * bookings, plus — when the page has Google Calendar sync on and its owner still has a usable
 * token — their Google freebusy over the same range. A freebusy failure degrades to "no extra
 * busy intervals" rather than failing the caller; Google sync is always optional (spec decision
 * 3).
 */
async function computeBusy(
  db: Db,
  page: BookingPage,
  range: { from: Date; to: Date },
): Promise<Interval[]> {
  const busy = await bookingService.bookedIntervals(db, page.id, range)
  if (!page.googleSync) return busy

  try {
    const token = await getGoogleAccessToken(page.ownerId)
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

/** Best-effort Google Calendar event create for a freshly booked slot; stores the event id on the
 * booking so cancel/reschedule can clean it up later. Never throws — a sync failure must not
 * fail a booking that already succeeded (spec decision 3: "Failures degrade gracefully"). */
async function syncGoogleEventCreate(
  db: Db,
  page: BookingPage,
  bookingId: string,
  input: { startAt: string; endAt: string; attendeeEmail: string },
): Promise<void> {
  if (!page.googleSync) return
  try {
    const token = await getGoogleAccessToken(page.ownerId)
    if (!token) return
    const { eventId } = await createCalendarClient().createEvent(token, {
      summary: page.title,
      description: page.description,
      start: input.startAt,
      end: input.endAt,
      attendeeEmail: input.attendeeEmail,
      timezone: page.timezone,
    })
    await bookingService.setGoogleEventId(db, bookingId, eventId)
  } catch (err) {
    console.error('[bookings.functions] Google event create failed', err)
  }
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
    await syncGoogleEventCreate(db, page, result.bookingId, {
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
 * owner (checked inside `getBookingForManage`/`cancelBooking`/`rescheduleBooking` themselves). */
function manageAuthFor(
  token: string | undefined,
  userId: string | null,
): { token: string } | { ownerId: string } {
  return token ? { token } : { ownerId: userId ?? '' }
}

export const getManagedBooking = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getManagedBooking)
  .validator(manageBookingSchema)
  .handler(async ({ data, context }) => {
    const userId = context.session?.user.id ?? null
    return bookingService.getBookingForManage(
      getDb(),
      data.bookingId,
      manageAuthFor(data.token, userId),
    )
  })

export const cancelBooking = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.cancelBooking)
  .validator(manageBookingSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    const userId = context.session?.user.id ?? null
    const view = await bookingService.getBookingForManage(
      db,
      data.bookingId,
      manageAuthFor(data.token, userId),
    )
    const by: CancelledBy = data.token ? 'visitor' : 'organiser'

    const result = await cancelViaRoom(view.page.id, data.bookingId, by)

    if (result.changed) {
      const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, data.bookingId) })
      if (booking?.googleEventId) {
        const page = await db.query.bookingPages.findFirst({
          where: eq(bookingPages.id, view.page.id),
        })
        if (page) {
          try {
            const token = await getGoogleAccessToken(page.ownerId)
            if (token) await createCalendarClient().deleteEvent(token, booking.googleEventId)
          } catch (err) {
            console.error('[bookings.functions] Google event delete failed', err)
          }
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
    const userId = context.session?.user.id ?? null
    const view = await bookingService.getBookingForManage(
      db,
      data.bookingId,
      manageAuthFor(data.token, userId),
    )

    const page = await requireDbPage(db, view.page.id)
    const startMs = new Date(data.startAt).getTime()
    const range = { from: new Date(startMs - 86_400_000), to: new Date(startMs + 86_400_000) }
    const busy = await computeBusy(db, page, range)

    const result = await rescheduleViaRoom(page.id, data.bookingId, data.startAt, busy)

    const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, data.bookingId) })
    if (page.googleSync && booking) {
      try {
        const token = await getGoogleAccessToken(page.ownerId)
        if (token) {
          const calendar = createCalendarClient()
          if (booking.googleEventId) await calendar.deleteEvent(token, booking.googleEventId)

          const endAt = new Date(startMs + page.slotDurationMin * 60_000).toISOString()
          const { eventId } = await calendar.createEvent(token, {
            summary: page.title,
            description: page.description,
            start: data.startAt,
            end: endAt,
            attendeeEmail: booking.visitorEmail,
            timezone: page.timezone,
          })
          await bookingService.setGoogleEventId(db, data.bookingId, eventId)
        }
      } catch (err) {
        console.error('[bookings.functions] Google event reschedule sync failed', err)
      }
    }

    // Reschedules initiated by the visitor still have their manage token in hand (`data.token`) —
    // pass it along so the confirmation email's manage link keeps working. An owner-initiated
    // reschedule has no plaintext token to hand back (only its hash is stored), so the email falls
    // back to the bare booking URL (see `sendBookingEmails`'s doc comment).
    await sendBookingEmails(env, 'rescheduled', data.bookingId, { db, manageToken: data.token })

    return result
  })

export const listPageBookings = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.listPageBookings)
  .validator(z.object({ pageId: z.string(), from: z.iso.datetime(), to: z.iso.datetime() }))
  .handler(async ({ data, context }) => {
    return bookingService.listBookings(getDb(), data.pageId, context.session.user.id, {
      from: new Date(data.from),
      to: new Date(data.to),
    })
  })
