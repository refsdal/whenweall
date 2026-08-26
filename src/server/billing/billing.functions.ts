import { createServerFn } from '@tanstack/react-start'
import { eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { member, subscription } from '#/server/db/schema'
import { requireOrgMiddleware, requireOwnerRole } from '#/server/auth/org'

/*
 * Same "declare once, reuse for both the function and the manifest" convention as
 * `pages.functions.ts`/`polls.functions.ts` — see the comment there.
 */
const REQUIRE_ORG = [requireOrgMiddleware] as const

export const SERVER_FN_MIDDLEWARE = {
  getBillingSnapshot: REQUIRE_ORG,
} as const

const ACTIVE_STATUSES = new Set(['active', 'trialing'])

export type BillingSnapshot = {
  subscription: { status: string; periodEnd: number | null; cancelAtPeriodEnd: boolean } | null
  seatsUsed: number
}

/**
 * Raw billing display data for the settings page — deliberately separate from
 * `getEntitlements` (spec: `entitlements.ts` is the ONE place plan RULES live; this is just
 * data to render, not a rule). Owner-only, same gate as the section it feeds.
 *
 * Seat usage is a plain member count (not members + pending invitations, unlike the seat *gate*
 * in `auth.ts`'s `assertSeatAvailable`) — simplest number an owner can sanity-check against
 * their roster, fetched here in the settings route loader rather than folded into every
 * `getSession` call, since it's only ever needed on this one page.
 */
export const getBillingSnapshot = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getBillingSnapshot)
  .handler(async ({ context }): Promise<BillingSnapshot> => {
    requireOwnerRole(context.org.role)
    const db = getDb()
    const [rows, members] = await Promise.all([
      db.query.subscription.findMany({ where: eq(subscription.referenceId, context.org.id) }),
      db.query.member.findMany({ where: eq(member.organizationId, context.org.id) }),
    ])
    const active = rows.find((s) => s.plan === 'premium' && ACTIVE_STATUSES.has(s.status)) ?? null

    return {
      subscription: active
        ? {
            status: active.status,
            periodEnd: active.periodEnd ? active.periodEnd.getTime() : null,
            cancelAtPeriodEnd: active.cancelAtPeriodEnd ?? false,
          }
        : null,
      seatsUsed: members.length,
    }
  })
