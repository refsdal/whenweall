import { and, count, eq, gte, isNull, ne } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { bookingPages, bookings, organization, polls, user } from '#/server/db/schema'
import { isActivePremium } from '#/server/billing/entitlements'

/** A total, plus how much of it arrived recently. */
export type Count = { total: number; last7: number; last30: number }

export type AdminStats = {
  growth: {
    users: Count
    orgs: Count
    polls: Count
    pollsFinalized: number
    signupSheets: Count
    bookingPages: Count
    bookings: Count
  }
  revenue: {
    totalOrgs: number
    premiumOrgs: number
    activeSubscriptions: number
    /** In minor units of the billing currency (øre), matching how Stripe reports amounts. */
    mrrMinor: number
    cancellingAtPeriodEnd: number
  }
}

/** Premium is 60 NOK/month; the yearly plan is 600 NOK, which is 50 NOK/month of recurring
 * revenue. Both are tax-inclusive (see the price's `tax_behavior`), so this is gross. */
const MONTHLY_PRICE_MINOR = 6000
const YEARLY_PRICE_MONTHLY_EQUIVALENT_MINOR = 5000

function isoDaysAgo(now: Date, days: number): string {
  return new Date(now.getTime() - days * 86_400_000).toISOString()
}

/** `createdAt` on the app's own tables is an ISO-8601 text column, so string comparison is
 * chronological and `gte` works directly. The Better-Auth tables use epoch-ms integers instead,
 * hence the two shapes below. */
async function textCount(
  db: Db,
  table: typeof polls | typeof bookingPages | typeof bookings,
  now: Date,
  extra?: ReturnType<typeof and>,
): Promise<Count> {
  const at = (days?: number) =>
    db
      .select({ n: count() })
      .from(table)
      .where(days === undefined ? extra : and(extra, gte(table.createdAt, isoDaysAgo(now, days))))
      .then((r) => r[0]?.n ?? 0)

  const [total, last7, last30] = await Promise.all([at(), at(7), at(30)])
  return { total, last7, last30 }
}

async function timestampCount(
  db: Db,
  table: typeof user | typeof organization,
  now: Date,
): Promise<Count> {
  const at = (days?: number) =>
    db
      .select({ n: count() })
      .from(table)
      .where(
        days === undefined
          ? undefined
          : gte(table.createdAt, new Date(now.getTime() - days * 86_400_000)),
      )
      .then((r) => r[0]?.n ?? 0)

  const [total, last7, last30] = await Promise.all([at(), at(7), at(30)])
  return { total, last7, last30 }
}

/**
 * Everything the admin dashboard shows, in one pass.
 *
 * Deliberately plain `COUNT` queries rather than any aggregation layer: at current scale this is
 * free, and pre-aggregating would be inventing a problem. If it ever gets slow, cache this
 * function's output — do not restructure the data.
 */
export async function getAdminStats(db: Db, now: Date = new Date()): Promise<AdminStats> {
  const live = isNull(polls.deletedAt)

  const [users, orgs, allPolls, signupSheets, pages, bookingCount, finalized, subs, orgTotal] =
    await Promise.all([
      timestampCount(db, user, now),
      timestampCount(db, organization, now),
      textCount(db, polls, now, and(live, ne(polls.type, 'signup'))),
      textCount(db, polls, now, and(live, eq(polls.type, 'signup'))),
      textCount(db, bookingPages, now, isNull(bookingPages.deletedAt)),
      textCount(db, bookings, now, undefined),
      db
        .select({ n: count() })
        .from(polls)
        .where(and(live, eq(polls.status, 'finalized')))
        .then((r) => r[0]?.n ?? 0),
      db.query.subscription.findMany(),
      db
        .select({ n: count() })
        .from(organization)
        .then((r) => r[0]?.n ?? 0),
    ])

  // `isActivePremium` is the one place plan rules live (see entitlements.ts). Reused rather than
  // re-derived: a dashboard that disagreed with the entitlement gate about who is Premium would
  // be worse than no dashboard at all.
  const activeSubs = subs.filter(isActivePremium)
  const premiumOrgIds = new Set(activeSubs.map((s) => s.referenceId))

  const mrrMinor = activeSubs.reduce(
    (sum, s) =>
      sum +
      (s.billingInterval === 'year' ? YEARLY_PRICE_MONTHLY_EQUIVALENT_MINOR : MONTHLY_PRICE_MINOR),
    0,
  )

  return {
    growth: {
      users,
      orgs,
      polls: allPolls,
      pollsFinalized: finalized,
      signupSheets,
      bookingPages: pages,
      bookings: bookingCount,
    },
    revenue: {
      totalOrgs: orgTotal,
      premiumOrgs: premiumOrgIds.size,
      activeSubscriptions: activeSubs.length,
      mrrMinor,
      cancellingAtPeriodEnd: activeSubs.filter((s) => s.cancelAtPeriodEnd).length,
    },
  }
}
