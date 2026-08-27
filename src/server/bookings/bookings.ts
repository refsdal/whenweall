import { and, eq, gt, lt } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import {
  bookingPages,
  bookings,
  organization,
  type Booking,
  type BookingPage,
  type CancelledBy,
} from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { isSlotAvailable, type Interval, type PageRules } from '#/lib/availability'
import { newId } from '#/lib/ids'
import { generateToken, hashToken, verifyToken } from '#/lib/tokens'
import { canManageContent, type OrgRole } from '#/server/auth/org-roles'
import type { BookingForManage, BookingView } from './viewmodel'

/** The acting org + role, as `requireOrgMiddleware` produces it. */
export type ActingOrg = { id: string; role: OrgRole }

export function pageRulesFrom(page: BookingPage): PageRules {
  return {
    timezone: page.timezone,
    slotDurationMin: page.slotDurationMin,
    bufferBeforeMin: page.bufferBeforeMin,
    bufferAfterMin: page.bufferAfterMin,
    minNoticeMin: page.minNoticeMin,
    maxDaysAhead: page.maxDaysAhead,
    availability: JSON.parse(page.availability),
    dateOverrides: page.dateOverrides ? JSON.parse(page.dateOverrides) : null,
  }
}

function toBookingView(b: Booking): BookingView {
  return {
    id: b.id,
    pageId: b.pageId,
    startAt: b.startAt,
    endAt: b.endAt,
    visitorName: b.visitorName,
    visitorEmail: b.visitorEmail,
    visitorNote: b.visitorNote,
    visitorTimezone: b.visitorTimezone,
    visitorLocale: b.visitorLocale,
    status: b.status,
    cancelledBy: b.cancelledBy,
    createdAt: b.createdAt,
  }
}

/**
 * Confirmed bookings on `page` overlapping `[range.from, range.to)`, as their *raw* stored
 * `[start_at, end_at)` — no buffer applied here. `generateSlots`/`isSlotAvailable` is the single
 * place page buffers are applied (it independently pads whichever *candidate* slot it's checking
 * by `bufferBeforeMin`/`bufferAfterMin` before comparing against the busy list): expanding a
 * booking's interval by the buffer here too would double it — a slot exactly `bufferAfterMin`
 * after a booking's end would need `2×bufferAfterMin` of clearance instead of the configured one.
 * `excludeBookingId` drops one booking (its own prior interval) so a reschedule doesn't collide
 * with the slot it's moving away from.
 */
async function bookedIntervalsForPage(
  db: Db,
  page: BookingPage,
  range: { from: Date; to: Date },
  excludeBookingId?: string,
): Promise<Interval[]> {
  const rows = await db.query.bookings.findMany({
    where: and(
      eq(bookings.pageId, page.id),
      eq(bookings.status, 'confirmed'),
      lt(bookings.startAt, range.to.toISOString()),
      gt(bookings.endAt, range.from.toISOString()),
    ),
  })

  return rows
    .filter((b) => b.id !== excludeBookingId)
    .map((b) => ({ start: b.startAt, end: b.endAt }))
}

export async function bookedIntervals(
  db: Db,
  pageId: string,
  range: { from: Date; to: Date },
): Promise<Interval[]> {
  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
  if (!page) return []
  return bookedIntervalsForPage(db, page, range)
}

export type CreateBookingInput = {
  startAt: string
  name: string
  email: string
  note?: string | null
  locale?: string | null
  timezone: string
}

export async function createBooking(
  db: Db,
  pageId: string,
  input: CreateBookingInput,
  busy: Interval[],
  now: Date = new Date(),
): Promise<{ bookingId: string; manageToken: string }> {
  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
  if (!page || page.deletedAt) throw new AppError('NOT_FOUND')
  if (page.status === 'paused') throw new AppError('PAGE_PAUSED')

  const startMs = new Date(input.startAt).getTime()
  if (startMs < now.getTime()) throw new AppError('BOOKING_PAST')

  const around = { from: new Date(startMs - 86_400_000), to: new Date(startMs + 86_400_000) }
  const existing = await bookedIntervalsForPage(db, page, around)
  const rules = pageRulesFrom(page)

  if (!isSlotAvailable(rules, input.startAt, { now, busy: [...busy, ...existing] })) {
    throw new AppError('SLOT_UNAVAILABLE')
  }

  const endAt = new Date(startMs + page.slotDurationMin * 60_000).toISOString()
  const manageToken = generateToken()
  const manageTokenHash = await hashToken(manageToken)
  const bookingId = newId()
  const nowIso = new Date().toISOString()

  await db.insert(bookings).values({
    id: bookingId,
    pageId,
    startAt: input.startAt,
    endAt,
    visitorName: input.name,
    visitorEmail: input.email,
    visitorNote: input.note ?? null,
    visitorLocale: input.locale ?? null,
    visitorTimezone: input.timezone,
    status: 'confirmed',
    cancelledBy: null,
    manageTokenHash,
    googleEventId: null,
    createdAt: nowIso,
    updatedAt: nowIso,
  })

  return { bookingId, manageToken }
}

/** Idempotent: cancelling an already-cancelled booking is a no-op, not an error. */
export async function cancelBooking(
  db: Db,
  bookingId: string,
  by: CancelledBy,
): Promise<{ changed: boolean }> {
  const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, bookingId) })
  if (!booking) throw new AppError('NOT_FOUND')
  if (booking.status === 'cancelled') return { changed: false }

  const now = new Date().toISOString()
  await db
    .update(bookings)
    .set({ status: 'cancelled', cancelledBy: by, updatedAt: now })
    .where(eq(bookings.id, bookingId))

  return { changed: true }
}

export async function rescheduleBooking(
  db: Db,
  bookingId: string,
  newStartAt: string,
  busy: Interval[],
  now: Date = new Date(),
): Promise<{ changed: true; previousStartAt: string }> {
  const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, bookingId) })
  if (!booking) throw new AppError('NOT_FOUND')
  if (booking.status === 'cancelled') throw new AppError('CONFLICT')

  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, booking.pageId) })
  if (!page || page.deletedAt) throw new AppError('NOT_FOUND')
  if (page.status === 'paused') throw new AppError('PAGE_PAUSED')

  const startMs = new Date(newStartAt).getTime()
  if (startMs < now.getTime()) throw new AppError('BOOKING_PAST')

  const around = { from: new Date(startMs - 86_400_000), to: new Date(startMs + 86_400_000) }
  const existing = await bookedIntervalsForPage(db, page, around, bookingId)
  const rules = pageRulesFrom(page)

  if (!isSlotAvailable(rules, newStartAt, { now, busy: [...busy, ...existing] })) {
    throw new AppError('SLOT_UNAVAILABLE')
  }

  const endAt = new Date(startMs + page.slotDurationMin * 60_000).toISOString()
  const nowIso = new Date().toISOString()
  const previousStartAt = booking.startAt

  await db.batch([
    db
      .update(bookings)
      .set({ startAt: newStartAt, endAt, updatedAt: nowIso })
      .where(eq(bookings.id, bookingId)),
  ])

  return { changed: true, previousStartAt }
}

/**
 * Auth: a visitor's manage token always works; the org path additionally requires the page to
 * belong to the caller's active org (NOT_FOUND otherwise — no leaking whether a booking id exists
 * outside the caller's own org) and a role that can manage it (FORBIDDEN — a plain member acting
 * on a page they didn't create).
 */
export async function getBookingForManage(
  db: Db,
  bookingId: string,
  auth: { token: string } | { org: ActingOrg; userId: string },
): Promise<BookingForManage> {
  const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, bookingId) })
  if (!booking) throw new AppError('NOT_FOUND')

  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, booking.pageId) })
  if (!page) throw new AppError('NOT_FOUND')

  if ('token' in auth) {
    const valid = await verifyToken(auth.token, booking.manageTokenHash)
    if (!valid) throw new AppError('INVALID_TOKEN')
  } else {
    if (page.organizationId !== auth.org.id) throw new AppError('NOT_FOUND')
    if (!canManageContent(auth.org, auth.userId, page.createdBy)) throw new AppError('FORBIDDEN')
  }

  const org = await db.query.organization.findFirst({
    where: eq(organization.id, page.organizationId),
  })

  return {
    ...toBookingView(booking),
    page: {
      id: page.id,
      handle: org?.slug ?? null,
      slug: page.slug,
      title: page.title,
      location: page.location,
      timezone: page.timezone,
      slotDurationMin: page.slotDurationMin,
      owner: { name: org?.name ?? '' },
    },
  }
}

/** Records the Google Calendar event id created for a booking (best-effort sync — see
 * `bookings.functions.ts`), updates it after a reschedule re-creates the event, or clears it
 * (`null`) once the event has been deleted (cancel, or a reschedule where sync is now off). */
export async function setGoogleEventId(
  db: Db,
  bookingId: string,
  eventId: string | null,
): Promise<void> {
  await db
    .update(bookings)
    .set({ googleEventId: eventId, updatedAt: new Date().toISOString() })
    .where(eq(bookings.id, bookingId))
}

export async function listBookings(
  db: Db,
  pageId: string,
  org: ActingOrg,
  userId: string,
  range: { from: Date; to: Date },
): Promise<BookingView[]> {
  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
  if (!page || page.deletedAt || page.organizationId !== org.id) throw new AppError('NOT_FOUND')
  if (!canManageContent(org, userId, page.createdBy)) throw new AppError('FORBIDDEN')

  const rows = await db.query.bookings.findMany({
    where: and(
      eq(bookings.pageId, pageId),
      lt(bookings.startAt, range.to.toISOString()),
      gt(bookings.endAt, range.from.toISOString()),
    ),
    orderBy: (b, { asc }) => [asc(b.startAt)],
  })

  return rows.map(toBookingView)
}
