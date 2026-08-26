import { and, eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { invitation, member, subscription } from '#/server/db/schema'

export const PREMIUM_MAX_SEATS = 10
export type Entitlements = {
  plan: 'free' | 'premium'
  maxSeats: 1 | typeof PREMIUM_MAX_SEATS
  googleSync: boolean
  branding: boolean
}

const ACTIVE_STATUSES = new Set(['active', 'trialing'])

/** The entitlements every org starts with — also the fallback used wherever an org can't be
 * resolved (e.g. `buildClientSession` for a session with no active org). */
export const FREE_ENTITLEMENTS: Entitlements = {
  plan: 'free',
  maxSeats: 1,
  googleSync: false,
  branding: false,
}

const PREMIUM_ENTITLEMENTS: Entitlements = {
  plan: 'premium',
  maxSeats: PREMIUM_MAX_SEATS,
  googleSync: true,
  branding: true,
}

/** Spec §4: the ONE place plan rules live. Reads D1 only — never Stripe. */
export async function getEntitlements(db: Db, orgId: string): Promise<Entitlements> {
  const rows = await db.query.subscription.findMany({
    where: eq(subscription.referenceId, orgId),
  })
  const premium = rows.some((s) => s.plan === 'premium' && ACTIVE_STATUSES.has(s.status))
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
      where: and(eq(invitation.organizationId, orgId), eq(invitation.status, 'pending')),
    }),
  ])
  return members.length + pendingInvitations.length
}
