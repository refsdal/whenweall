import { createServerFn } from '@tanstack/react-start'
import { eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { subscription } from '#/server/db/schema'
import { requireOrgMiddleware } from '#/server/auth/org'
import { requireOwnerRole } from '#/server/auth/org-roles'
import { getSeatsUsed, isActivePremium } from '#/server/billing/entitlements'

/*
 * Same "declare once, reuse for both the function and the manifest" convention as
 * `pages.functions.ts`/`polls.functions.ts` — see the comment there.
 */
const REQUIRE_ORG = [requireOrgMiddleware] as const

export const SERVER_FN_MIDDLEWARE = {
  getBillingSnapshot: REQUIRE_ORG,
} as const

export type BillingSnapshot = {
  subscription: { status: string; periodEnd: number | null; cancelAtPeriodEnd: boolean } | null
  seatsUsed: number
}

/**
 * Raw billing display data for the settings page — deliberately separate from
 * `getEntitlements` (spec: `entitlements.ts` is the ONE place plan RULES live; this is just
 * data to render, not a rule). Owner-only, same gate as the section it feeds.
 *
 * Seat usage is `getSeatsUsed` (members + pending invitations) — the same count the invite seat
 * *gate* in `auth.ts`'s `assertSeatAvailable` enforces, so an owner reading "6 of 10" here sees
 * exactly why their next invite would (or wouldn't) be blocked, fetched here in the settings
 * route loader rather than folded into every `getSession` call, since it's only ever needed on
 * this one page.
 */
export const getBillingSnapshot = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getBillingSnapshot)
  .handler(async ({ context }): Promise<BillingSnapshot> => {
    requireOwnerRole(context.org.role)
    const db = getDb()
    const [rows, seatsUsed] = await Promise.all([
      db.query.subscription.findMany({ where: eq(subscription.referenceId, context.org.id) }),
      getSeatsUsed(db, context.org.id),
    ])
    const active = rows.find(isActivePremium) ?? null

    return {
      subscription: active
        ? {
            status: active.status,
            periodEnd: active.periodEnd ? active.periodEnd.getTime() : null,
            cancelAtPeriodEnd: active.cancelAtPeriodEnd ?? false,
          }
        : null,
      seatsUsed,
    }
  })
