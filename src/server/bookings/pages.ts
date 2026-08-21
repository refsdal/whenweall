import { and, eq, gte, isNull } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { bookingPages, bookings, user, type BookingPage } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { newId } from '#/lib/ids'
import type { CreateBookingPageInput, UpdateBookingPageInput } from './schemas'
import type { PageSummary, PageView, PublicPageView } from './viewmodel'

/**
 * D1/SQLite reports a unique-index violation as a `DrizzleQueryError` whose own `message` is just
 * "Failed query: ..." — the actual "UNIQUE constraint failed" text is on `.cause` (itself wrapping
 * a D1 error whose `.cause` is the underlying SQLITE_CONSTRAINT). There's no distinct error class
 * to catch, so callers that want to translate the violation into a domain error (e.g.
 * `SLUG_TAKEN`, `HANDLE_TAKEN`) walk the `cause` chain matching on message text instead.
 */
function isUniqueConstraintError(err: unknown): boolean {
  let cur: unknown = err
  for (let i = 0; i < 5 && cur; i++) {
    if (cur instanceof Error) {
      if (/unique constraint/i.test(cur.message)) return true
      cur = cur.cause
    } else {
      break
    }
  }
  return false
}

function toPageView(page: BookingPage): PageView {
  return {
    id: page.id,
    slug: page.slug,
    title: page.title,
    description: page.description,
    location: page.location,
    timezone: page.timezone,
    slotDurationMin: page.slotDurationMin,
    bufferBeforeMin: page.bufferBeforeMin,
    bufferAfterMin: page.bufferAfterMin,
    minNoticeMin: page.minNoticeMin,
    maxDaysAhead: page.maxDaysAhead,
    availability: JSON.parse(page.availability),
    dateOverrides: page.dateOverrides ? JSON.parse(page.dateOverrides) : null,
    googleSync: page.googleSync,
    reminders: page.reminders,
    status: page.status,
    createdAt: page.createdAt,
    updatedAt: page.updatedAt,
  }
}

export async function createPage(
  db: Db,
  ownerId: string,
  input: CreateBookingPageInput,
): Promise<{ id: string }> {
  const id = newId()
  const now = new Date().toISOString()

  try {
    await db.insert(bookingPages).values({
      id,
      ownerId,
      slug: input.slug,
      title: input.title,
      description: input.description ?? null,
      location: input.location ?? null,
      timezone: input.timezone,
      slotDurationMin: input.slotDurationMin,
      bufferBeforeMin: input.bufferBeforeMin,
      bufferAfterMin: input.bufferAfterMin,
      minNoticeMin: input.minNoticeMin,
      maxDaysAhead: input.maxDaysAhead,
      availability: JSON.stringify(input.availability),
      dateOverrides: input.dateOverrides ? JSON.stringify(input.dateOverrides) : null,
      googleSync: input.googleSync,
      reminders: input.reminders,
      status: 'active',
      createdAt: now,
      updatedAt: now,
    })
  } catch (err) {
    if (isUniqueConstraintError(err)) throw new AppError('SLUG_TAKEN')
    throw err
  }

  return { id }
}

export async function requireOwnedPage(
  db: Db,
  pageId: string,
  ownerId: string,
): Promise<BookingPage> {
  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
  if (!page || page.deletedAt) throw new AppError('NOT_FOUND')
  if (page.ownerId !== ownerId) throw new AppError('FORBIDDEN')
  return page
}

export async function updatePage(
  db: Db,
  pageId: string,
  ownerId: string,
  input: Omit<UpdateBookingPageInput, 'pageId'>,
): Promise<void> {
  await requireOwnedPage(db, pageId, ownerId)
  const now = new Date().toISOString()

  const set: Partial<typeof bookingPages.$inferInsert> = { updatedAt: now }
  if (input.slug !== undefined) set.slug = input.slug
  if (input.title !== undefined) set.title = input.title
  if (input.description !== undefined) set.description = input.description ?? null
  if (input.location !== undefined) set.location = input.location ?? null
  if (input.timezone !== undefined) set.timezone = input.timezone
  if (input.slotDurationMin !== undefined) set.slotDurationMin = input.slotDurationMin
  if (input.bufferBeforeMin !== undefined) set.bufferBeforeMin = input.bufferBeforeMin
  if (input.bufferAfterMin !== undefined) set.bufferAfterMin = input.bufferAfterMin
  if (input.minNoticeMin !== undefined) set.minNoticeMin = input.minNoticeMin
  if (input.maxDaysAhead !== undefined) set.maxDaysAhead = input.maxDaysAhead
  if (input.availability !== undefined) set.availability = JSON.stringify(input.availability)
  if (input.dateOverrides !== undefined) {
    set.dateOverrides = input.dateOverrides ? JSON.stringify(input.dateOverrides) : null
  }
  if (input.googleSync !== undefined) set.googleSync = input.googleSync
  if (input.reminders !== undefined) set.reminders = input.reminders
  if (input.status !== undefined) set.status = input.status

  try {
    await db.update(bookingPages).set(set).where(eq(bookingPages.id, pageId))
  } catch (err) {
    if (isUniqueConstraintError(err)) throw new AppError('SLUG_TAKEN')
    throw err
  }
}

export async function deletePage(db: Db, pageId: string, ownerId: string): Promise<void> {
  await requireOwnedPage(db, pageId, ownerId)
  const now = new Date().toISOString()
  await db
    .update(bookingPages)
    .set({ deletedAt: now, updatedAt: now })
    .where(eq(bookingPages.id, pageId))
}

export async function listMyPages(db: Db, ownerId: string): Promise<PageSummary[]> {
  const now = new Date().toISOString()
  const rows = await db.query.bookingPages.findMany({
    where: and(eq(bookingPages.ownerId, ownerId), isNull(bookingPages.deletedAt)),
    orderBy: (p, { desc }) => [desc(p.createdAt)],
    with: {
      bookings: {
        where: and(eq(bookings.status, 'confirmed'), gte(bookings.startAt, now)),
      },
    },
  })

  return rows.map((p) => ({
    id: p.id,
    slug: p.slug,
    title: p.title,
    status: p.status,
    upcomingCount: p.bookings.length,
    createdAt: p.createdAt,
    updatedAt: p.updatedAt,
  }))
}

export async function getOwnedPage(db: Db, pageId: string, ownerId: string): Promise<PageView> {
  const page = await requireOwnedPage(db, pageId, ownerId)
  return toPageView(page)
}

/**
 * Public lookup for `/book/<handle>/<slug>`. Returns the page (including a paused `status`, so
 * the route can show a "not currently accepting bookings" message) for any handle/slug pair that
 * resolves to a page that hasn't been soft-deleted — `createBooking` is what actually rejects a
 * paused page (`PAGE_PAUSED`), not this lookup.
 */
export async function getPublicPage(
  db: Db,
  handle: string,
  slug: string,
): Promise<PublicPageView | null> {
  const owner = await db.query.user.findFirst({ where: eq(user.handle, handle) })
  if (!owner) return null

  const page = await db.query.bookingPages.findFirst({
    where: and(eq(bookingPages.ownerId, owner.id), eq(bookingPages.slug, slug)),
  })
  if (!page || page.deletedAt) return null

  return {
    id: page.id,
    slug: page.slug,
    title: page.title,
    description: page.description,
    location: page.location,
    timezone: page.timezone,
    slotDurationMin: page.slotDurationMin,
    bufferBeforeMin: page.bufferBeforeMin,
    bufferAfterMin: page.bufferAfterMin,
    minNoticeMin: page.minNoticeMin,
    maxDaysAhead: page.maxDaysAhead,
    availability: JSON.parse(page.availability),
    dateOverrides: page.dateOverrides ? JSON.parse(page.dateOverrides) : null,
    status: page.status,
    owner: { name: owner.name },
  }
}

/** `disconnectGoogleCalendar` server fn: turns off `googleSync` on every page this owner has —
 * the linked Google account itself is unlinked separately (via settings/Better-Auth), so this is
 * just the booking-side switch that stops calendar reads/writes for their pages. */
export async function disconnectGoogleSync(db: Db, ownerId: string): Promise<void> {
  await db
    .update(bookingPages)
    .set({ googleSync: false, updatedAt: new Date().toISOString() })
    .where(eq(bookingPages.ownerId, ownerId))
}

export async function setUserHandle(db: Db, userId: string, handle: string): Promise<void> {
  try {
    await db.update(user).set({ handle }).where(eq(user.id, userId))
  } catch (err) {
    if (isUniqueConstraintError(err)) throw new AppError('HANDLE_TAKEN')
    throw err
  }
}
