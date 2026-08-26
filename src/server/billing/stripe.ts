import Stripe from 'stripe'
import { and, eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { member } from '#/server/db/schema'

/** Workers has no Node http — stripe-node must use its fetch client.
 *
 * `apiVersion` is intentionally omitted: stripe-node's types pin it to the literal
 * `LatestApiVersion` baked into the installed SDK major (currently `2026-07-29.dahlia`), which
 * drifts every time `stripe` is upgraded. Omitting it makes the client use the account's
 * pinned/default API version instead — no hardcoded literal to keep in sync with the package. */
export function createStripeClient(secretKey: string): Stripe {
  return new Stripe(secretKey, {
    httpClient: Stripe.createFetchHttpClient(),
  })
}

/** Spec §3: only the org owner manages billing. */
export async function authorizeSubscriptionReference(
  db: Db,
  { userId, referenceId }: { userId: string; referenceId: string },
): Promise<boolean> {
  const m = await db.query.member.findFirst({
    where: and(eq(member.organizationId, referenceId), eq(member.userId, userId)),
  })
  return m?.role === 'owner'
}
