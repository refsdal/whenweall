import { and, eq, gte, isNull } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { bookingPages, bookings, organization, type BookingPage } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import {
  deleteScopeSubscriptions,
  ensureCreatorSubscription,
} from '#/server/notifications/subscriptions'
import { newId } from '#/lib/ids'
import { canManageContent, type OrgRole } from '#/server/auth/org'
import type { CreateBookingPageInput, UpdateBookingPageInput } from './schemas'
import type { PageSummary, PageView, PublicPageView } from './viewmodel'

/** The creator/org pair a page is created under. `createdBy` is nullable on the row (cleared if
 * the creator's account is later deleted), but a fresh creation always has one. `memberUserId`
 * defaults to `createdBy` when omitted — the page's calendar/availability starts as the
 * creator's own, reassignable later (e.g. handed off to a teammate). */
export type PageOwner = {
  organizationId: string
  createdBy: string | null
  memberUserId?: string | null
}

/** The acting org + role, as `requireOrgMiddleware` produces it. */
export type ActingOrg = { id: string; role: OrgRole }

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
  owner: PageOwner,
  input: CreateBookingPageInput,
): Promise<{ id: string }> {
  const id = newId()
  const now = new Date().toISOString()
  const memberUserId = owner.memberUserId !== undefined ? owner.memberUserId : owner.createdBy

  try {
    await db.insert(bookingPages).values({
      id,
      organizationId: owner.organizationId,
      createdBy: owner.createdBy,
      memberUserId,
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

  // Subscribes whoever's calendar this page books against — the same person who received the
  // unconditional organiser notice before notifications had preferences. Deliberately no fallback
  // to `createdBy`: `memberUserId` already defaults to the creator above, so a null here means the
  // caller explicitly asked for a page with no organiser, which has never sent organiser mail.
  await ensureCreatorSubscription(db, { type: 'booking_page', id }, memberUserId)

  return { id }
}

/**
 * NOT_FOUND when the page doesn't exist, is soft-deleted, or belongs to a different org (no
 * leaking whether a page id exists at all outside the caller's own org); FORBIDDEN when it's in
 * the right org but the caller can't manage it (a plain member managing someone else's page).
 */
export async function requireManagedPage(
  db: Db,
  pageId: string,
  org: ActingOrg,
  userId: string,
): Promise<BookingPage> {
  const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
  if (!page || page.deletedAt || page.organizationId !== org.id) throw new AppError('NOT_FOUND')
  if (!canManageContent(org, userId, page.createdBy)) throw new AppError('FORBIDDEN')
  return page
}

export async function updatePage(
  db: Db,
  pageId: string,
  org: ActingOrg,
  userId: string,
  input: Omit<UpdateBookingPageInput, 'pageId'>,
): Promise<void> {
  await requireManagedPage(db, pageId, org, userId)
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

export async function deletePage(
  db: Db,
  pageId: string,
  org: ActingOrg,
  userId: string,
): Promise<void> {
  await requireManagedPage(db, pageId, org, userId)
  const now = new Date().toISOString()
  await db
    .update(bookingPages)
    .set({ deletedAt: now, updatedAt: now })
    .where(eq(bookingPages.id, pageId))
  // Manual cascade — a polymorphic `scopeId` cannot carry a foreign key.
  await deleteScopeSubscriptions(db, { type: 'booking_page', id: pageId })
}

export async function listMyPages(db: Db, organizationId: string): Promise<PageSummary[]> {
  const now = new Date().toISOString()
  const rows = await db.query.bookingPages.findMany({
    where: and(eq(bookingPages.organizationId, organizationId), isNull(bookingPages.deletedAt)),
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

export async function getOwnedPage(
  db: Db,
  pageId: string,
  org: ActingOrg,
  userId: string,
): Promise<PageView> {
  const page = await requireManagedPage(db, pageId, org, userId)
  return toPageView(page)
}

/**
 * Public lookup for `/book/<handle>/<slug>`. Returns the page (including a paused `status`, so
 * the route can show a "not currently accepting bookings" message) for any handle/slug pair that
 * resolves to a page that hasn't been soft-deleted — `createBooking` is what actually rejects a
 * paused page (`PAGE_PAUSED`), not this lookup. `handle` is the organization's slug (formerly the
 * owning user's `handle`, before content ownership moved to organizations).
 */
export async function getPublicPage(
  db: Db,
  handle: string,
  slug: string,
): Promise<PublicPageView | null> {
  const org = await db.query.organization.findFirst({ where: eq(organization.slug, handle) })
  if (!org) return null

  const page = await db.query.bookingPages.findFirst({
    where: and(eq(bookingPages.organizationId, org.id), eq(bookingPages.slug, slug)),
  })
  if (!page || page.deletedAt) return null

  return {
    id: page.id,
    handle: org.slug,
    slug: page.slug,
    title: page.title,
    description: page.description,
    location: page.location,
    timezone: page.timezone,
    slotDurationMin: page.slotDurationMin,
    maxDaysAhead: page.maxDaysAhead,
    status: page.status,
    owner: { name: org.name },
  }
}

/** `disconnectGoogleCalendar` server fn: turns off `googleSync` on every page whose calendar is
 * this user's (`memberUserId`) — the linked Google account itself is unlinked separately (via
 * settings/Better-Auth), so this is just the booking-side switch that stops calendar reads/writes
 * for pages reading/writing their calendar. Deliberately scoped by `memberUserId`, not the org —
 * disconnecting *your* Google account must not silently turn off sync on a teammate's pages. */
export async function disconnectGoogleSync(db: Db, userId: string): Promise<void> {
  await db
    .update(bookingPages)
    .set({ googleSync: false, updatedAt: new Date().toISOString() })
    .where(eq(bookingPages.memberUserId, userId))
}

export async function setOrgSlug(db: Db, orgId: string, slug: string): Promise<void> {
  try {
    await db.update(organization).set({ slug }).where(eq(organization.id, orgId))
  } catch (err) {
    if (isUniqueConstraintError(err)) throw new AppError('HANDLE_TAKEN')
    throw err
  }
}
