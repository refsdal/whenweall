import { eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { subscription } from '#/server/db/schema'

export const PREMIUM_MAX_SEATS = 10
export type Entitlements = {
  plan: 'free' | 'premium'
  maxSeats: 1 | typeof PREMIUM_MAX_SEATS
  googleSync: boolean
  branding: boolean
}

const ACTIVE_STATUSES = new Set(['active', 'trialing'])

/** Spec §4: the ONE place plan rules live. Reads D1 only — never Stripe. */
export async function getEntitlements(db: Db, orgId: string): Promise<Entitlements> {
  const rows = await db.query.subscription.findMany({
    where: eq(subscription.referenceId, orgId),
  })
  const premium = rows.some((s) => s.plan === 'premium' && ACTIVE_STATUSES.has(s.status))
  return premium
    ? { plan: 'premium', maxSeats: PREMIUM_MAX_SEATS, googleSync: true, branding: true }
    : { plan: 'free', maxSeats: 1, googleSync: false, branding: false }
}
