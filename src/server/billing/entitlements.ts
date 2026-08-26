import { and, eq, gt } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { invitation, member, subscription } from '#/server/db/schema'
import { PREMIUM_PLAN_NAME } from '#/lib/billing'

export const PREMIUM_MAX_SEATS = 10
export type Entitlements = Readonly<{
  plan: 'free' | 'premium'
  maxSeats: 1 | typeof PREMIUM_MAX_SEATS
  googleSync: boolean
  branding: boolean
}>

const ACTIVE_STATUSES = new Set(['active', 'trialing'])

/** The entitlements every org starts with — also the fallback used wherever an org can't be
 * resolved (e.g. `buildClientSession` for a session with no active org). Frozen: these are
 * shared singletons handed straight to callers, not per-call copies. */
export const FREE_ENTITLEMENTS: Entitlements = Object.freeze({
  plan: 'free',
  maxSeats: 1,
  googleSync: false,
  branding: false,
})

const PREMIUM_ENTITLEMENTS: Entitlements = Object.freeze({
  plan: 'premium',
  maxSeats: PREMIUM_MAX_SEATS,
  googleSync: true,
  branding: true,
})

/** A `subscription` row's shape as far as plan-activeness is concerned — the one place that
 * decides whether a given row counts as an active Premium subscription. Shared by
 * `getEntitlements` (the plan RULE) and `billing.functions.ts`'s `getBillingSnapshot` (the plan
 * DISPLAY) so the two can never disagree about which row is "the" active one. */
export function isActivePremium(row: { plan: string; status: string }): boolean {
  return row.plan === PREMIUM_PLAN_NAME && ACTIVE_STATUSES.has(row.status)
}

/** Spec §4: the ONE place plan rules live. Reads D1 only — never Stripe. */
export async function getEntitlements(db: Db, orgId: string): Promise<Entitlements> {
  const rows = await db.query.subscription.findMany({
    where: eq(subscription.referenceId, orgId),
  })
  const premium = rows.some(isActivePremium)
  return premium ? PREMIUM_ENTITLEMENTS : FREE_ENTITLEMENTS
}

/** Occupied-seat count: members + pending invitations. Shared by `auth.ts`'s
 * `assertSeatAvailable` (the invite seat *gate*) and `billing.functions.ts`'s
 * `getBillingSnapshot` (the seat usage *displayed* to the owner) so the two numbers can't drift —
 * an owner reading "6 of 10" on the settings page must see the same count the gate is enforcing. */
export async function getSeatsUsed(db: Db, orgId: string): Promise<number> {
  const [members, pendingInvitations] = await Promise.all([
    db.query.member.findMany({ where: eq(member.organizationId, orgId) }),
    db.query.invitation.findMany({
      // Better-Auth's own accept-invitation endpoint refuses an invitation once
      // `expiresAt` has passed (see `acceptInvitation` in
      // node_modules/better-auth/dist/plugins/organization/routes/crud-invites.mjs) — without
      // this filter an expired invite could never be accepted or canceled, yet would occupy a
      // seat forever.
      where: and(
        eq(invitation.organizationId, orgId),
        eq(invitation.status, 'pending'),
        gt(invitation.expiresAt, new Date()),
      ),
    }),
  ])
  return members.length + pendingInvitations.length
}
