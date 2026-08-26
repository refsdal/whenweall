import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { subscription } from '#/server/db/schema'
import { createAuth } from '#/server/auth/auth'
import { createStripeClient } from '#/server/billing/stripe'
import { getEntitlements } from '#/server/billing/entitlements'
import { makeOrg, makeSubscription, makeUser } from '../../../../test/helpers'

const authEnv = { ...env, APP_ENV: 'test' } as never

/** `client.webhooks` is pure local HMAC signing/verification (no network call) — the same
 * `Stripe` instance `createStripeClient` builds for the app, reused here purely for its
 * `webhooks` helpers so signing exercises the fetch-http-client-based Workers crypto path
 * (`generateTestHeaderStringAsync`/`constructEventAsync`, since the Workers `SubtleCrypto`
 * provider only supports the async HMAC path — the sync methods throw
 * `CryptoProviderOnlySupportsAsyncError` in this runtime). */
const stripeClient = createStripeClient('sk_test_dummy')
const WEBHOOK_SECRET = 'whsec_dummy'
// Matches test/wrangler.test.jsonc's STRIPE_PRICE_PREMIUM_MONTHLY var — resolvePlanItem in
// @better-auth/stripe matches subscription items against configured plan price ids.
const MONTHLY_PRICE_ID = 'price_premium_monthly_dev'

async function signedWebhookRequest(event: Record<string, unknown>): Promise<Request> {
  const payload = JSON.stringify(event)
  const signature = await stripeClient.webhooks.generateTestHeaderStringAsync({
    payload,
    secret: WEBHOOK_SECRET,
  })
  return new Request('http://localhost/api/auth/stripe/webhook', {
    method: 'POST',
    headers: { 'content-type': 'application/json', 'stripe-signature': signature },
    body: payload,
  })
}

function subscriptionUpdatedEvent(opts: {
  stripeSubscriptionId: string
  stripeCustomerId: string
  status: string
}): Record<string, unknown> {
  const now = Math.floor(Date.now() / 1000)
  return {
    id: `evt_${crypto.randomUUID()}`,
    object: 'event',
    type: 'customer.subscription.updated',
    created: now,
    data: {
      object: {
        id: opts.stripeSubscriptionId,
        object: 'subscription',
        customer: opts.stripeCustomerId,
        status: opts.status,
        cancel_at_period_end: false,
        cancel_at: null,
        canceled_at: opts.status === 'canceled' ? now : null,
        ended_at: null,
        trial_start: null,
        trial_end: null,
        schedule: null,
        metadata: {},
        items: {
          object: 'list',
          data: [
            {
              id: `si_${crypto.randomUUID()}`,
              object: 'subscription_item',
              quantity: 1,
              current_period_start: now,
              current_period_end: now + 30 * 24 * 60 * 60,
              price: { id: MONTHLY_PRICE_ID, recurring: { interval: 'month' } },
            },
          ],
        },
      },
    },
  }
}

function subscriptionDeletedEvent(opts: {
  stripeSubscriptionId: string
  stripeCustomerId: string
}): Record<string, unknown> {
  const now = Math.floor(Date.now() / 1000)
  return {
    id: `evt_${crypto.randomUUID()}`,
    object: 'event',
    type: 'customer.subscription.deleted',
    created: now,
    data: {
      object: {
        id: opts.stripeSubscriptionId,
        object: 'subscription',
        customer: opts.stripeCustomerId,
        status: 'canceled',
        cancel_at_period_end: false,
        cancel_at: null,
        canceled_at: now,
        ended_at: now,
        trial_start: null,
        trial_end: null,
        metadata: {},
      },
    },
  }
}

describe('Stripe webhook (customer.subscription.*) at /api/auth/stripe/webhook', () => {
  it('customer.subscription.updated flips an active subscription to canceled, and getEntitlements demotes the org to free', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, ownerId)
    const stripeSubscriptionId = `sub_${crypto.randomUUID()}`
    const stripeCustomerId = `cus_${crypto.randomUUID()}`
    const { id: subId } = await makeSubscription(db, orgId, {
      plan: 'premium',
      status: 'active',
      stripeSubscriptionId,
      stripeCustomerId,
    })

    await expect(getEntitlements(db, orgId)).resolves.toMatchObject({ plan: 'premium' })

    const request = await signedWebhookRequest(
      subscriptionUpdatedEvent({ stripeSubscriptionId, stripeCustomerId, status: 'canceled' }),
    )
    const response = await auth.handler(request)
    expect(response.status).toBe(200)

    const row = await db.query.subscription.findFirst({ where: eq(subscription.id, subId) })
    expect(row?.status).toBe('canceled')

    await expect(getEntitlements(db, orgId)).resolves.toMatchObject({ plan: 'free', maxSeats: 1 })
  })

  it('customer.subscription.deleted flips an active subscription to canceled, and getEntitlements demotes the org to free', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, ownerId)
    const stripeSubscriptionId = `sub_${crypto.randomUUID()}`
    const stripeCustomerId = `cus_${crypto.randomUUID()}`
    const { id: subId } = await makeSubscription(db, orgId, {
      plan: 'premium',
      status: 'active',
      stripeSubscriptionId,
      stripeCustomerId,
    })

    await expect(getEntitlements(db, orgId)).resolves.toMatchObject({ plan: 'premium' })

    const request = await signedWebhookRequest(
      subscriptionDeletedEvent({ stripeSubscriptionId, stripeCustomerId }),
    )
    const response = await auth.handler(request)
    expect(response.status).toBe(200)

    const row = await db.query.subscription.findFirst({ where: eq(subscription.id, subId) })
    expect(row?.status).toBe('canceled')

    await expect(getEntitlements(db, orgId)).resolves.toMatchObject({ plan: 'free', maxSeats: 1 })
  })

  it('rejects a request with an invalid signature (400) and leaves the subscription row untouched', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, ownerId)
    const stripeSubscriptionId = `sub_${crypto.randomUUID()}`
    const stripeCustomerId = `cus_${crypto.randomUUID()}`
    const { id: subId } = await makeSubscription(db, orgId, {
      plan: 'premium',
      status: 'active',
      stripeSubscriptionId,
      stripeCustomerId,
    })

    const payload = JSON.stringify(
      subscriptionUpdatedEvent({ stripeSubscriptionId, stripeCustomerId, status: 'canceled' }),
    )
    const request = new Request('http://localhost/api/auth/stripe/webhook', {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'stripe-signature': 't=1,v1=deadbeef' },
      body: payload,
    })
    const response = await auth.handler(request)
    expect(response.status).toBe(400)

    const row = await db.query.subscription.findFirst({ where: eq(subscription.id, subId) })
    expect(row?.status).toBe('active')
  })
})
