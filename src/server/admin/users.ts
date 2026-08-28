import { and, count, desc, eq, isNull, like, or, sql } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import {
  adminAuditLog,
  bookingPages,
  bookings,
  member,
  organization,
  polls,
  user,
} from '#/server/db/schema'
import type { AdminAuditLogRow } from '#/server/db/schema'

export type AdminUserSummary = {
  id: string
  name: string
  email: string
  role: string | null
  banned: boolean
  emailVerified: boolean
  createdAt: string
}

export type AdminUserDetail = AdminUserSummary & {
  banReason: string | null
  orgs: { id: string; name: string; slug: string; role: string }[]
  counts: { polls: number; bookingPages: number; bookings: number }
  recentActions: AdminAuditLogRow[]
}

/** Never widen this to `select()`. The user row carries the password hash via `account`, and a
 * support screen has no business shipping credential material to a browser. */
const SUMMARY_COLUMNS = {
  id: user.id,
  name: user.name,
  email: user.email,
  role: user.role,
  banned: user.banned,
  emailVerified: user.emailVerified,
  createdAt: user.createdAt,
} as const

function toSummary(row: {
  id: string
  name: string
  email: string
  role: string | null
  banned: boolean | null
  emailVerified: boolean
  createdAt: Date
}): AdminUserSummary {
  return {
    id: row.id,
    name: row.name,
    email: row.email,
    role: row.role,
    banned: row.banned ?? false,
    emailVerified: row.emailVerified,
    createdAt: row.createdAt.toISOString(),
  }
}

/**
 * The support console's find-a-person query.
 *
 * Search is a substring match on email or name, lower-cased on both sides — someone reading a
 * ticket has a fragment of an address, not an exact one, and `user.email` is stored lower-case
 * anyway (Better-Auth normalises it on write).
 */
export async function listAdminUsers(
  db: Db,
  query: { search?: string; limit: number; offset: number },
): Promise<{ users: AdminUserSummary[]; total: number }> {
  const term = query.search?.trim().toLowerCase()
  const where = term
    ? or(like(sql`lower(${user.email})`, `%${term}%`), like(sql`lower(${user.name})`, `%${term}%`))
    : undefined

  const [rows, totals] = await Promise.all([
    db
      .select(SUMMARY_COLUMNS)
      .from(user)
      .where(where)
      .orderBy(desc(user.createdAt))
      .limit(query.limit)
      .offset(query.offset),
    db.select({ n: count() }).from(user).where(where),
  ])

  return { users: rows.map(toSummary), total: totals[0]?.n ?? 0 }
}

/** Returns `null` rather than throwing for an unknown id — a stale link in a ticket is an
 * ordinary occurrence, not an exceptional one. */
export async function getAdminUserDetail(db: Db, userId: string): Promise<AdminUserDetail | null> {
  const rows = await db.select(SUMMARY_COLUMNS).from(user).where(eq(user.id, userId)).limit(1)
  const row = rows[0]
  if (!row) return null

  const [banReasonRow, orgRows, pollCount, pageCount, bookingCount, recentActions] =
    await Promise.all([
      db.select({ banReason: user.banReason }).from(user).where(eq(user.id, userId)).limit(1),
      db
        .select({
          id: organization.id,
          name: organization.name,
          slug: organization.slug,
          role: member.role,
        })
        .from(member)
        .innerJoin(organization, eq(member.organizationId, organization.id))
        .where(eq(member.userId, userId)),
      db
        .select({ n: count() })
        .from(polls)
        .where(and(eq(polls.createdBy, userId), isNull(polls.deletedAt)))
        .then((r) => r[0]?.n ?? 0),
      db
        .select({ n: count() })
        .from(bookingPages)
        .where(and(eq(bookingPages.createdBy, userId), isNull(bookingPages.deletedAt)))
        .then((r) => r[0]?.n ?? 0),
      db
        .select({ n: count() })
        .from(bookings)
        .innerJoin(bookingPages, eq(bookings.pageId, bookingPages.id))
        .where(eq(bookingPages.createdBy, userId))
        .then((r) => r[0]?.n ?? 0),
      db.query.adminAuditLog.findMany({
        where: and(eq(adminAuditLog.targetType, 'user'), eq(adminAuditLog.targetId, userId)),
        orderBy: (t, { desc: d }) => [d(t.createdAt)],
        limit: 20,
      }),
    ])

  return {
    ...toSummary(row),
    banReason: banReasonRow[0]?.banReason ?? null,
    orgs: orgRows,
    counts: { polls: pollCount, bookingPages: pageCount, bookings: bookingCount },
    recentActions,
  }
}
