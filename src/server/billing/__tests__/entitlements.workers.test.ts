import { env } from 'cloudflare:workers'
import { describe, it, expect, beforeEach } from 'vitest'
import { createDb } from '#/server/db/client'
import { getEntitlements } from '../entitlements'
import { makeOrg, makeUser, makeSubscription } from '../../../../test/helpers'
import type { Db } from '#/server/db/client'

describe('entitlements', () => {
  let db: Db

  beforeEach(() => {
    db = createDb(env.DB)
  })

  it('free org (no subscription row) returns free entitlements', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)

    const entitlements = await getEntitlements(db, orgId)

    expect(entitlements).toEqual({
      plan: 'free',
      maxSeats: 1,
      googleSync: false,
      branding: false,
    })
  })

  it('active premium subscription returns premium entitlements', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'active' })

    const entitlements = await getEntitlements(db, orgId)

    expect(entitlements).toEqual({
      plan: 'premium',
      maxSeats: 10,
      googleSync: true,
      branding: true,
    })
  })

  it('trialing premium subscription counts as premium', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'trialing' })

    const entitlements = await getEntitlements(db, orgId)

    expect(entitlements).toEqual({
      plan: 'premium',
      maxSeats: 10,
      googleSync: true,
      branding: true,
    })
  })

  it('canceled subscription returns free entitlements', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'canceled' })

    const entitlements = await getEntitlements(db, orgId)

    expect(entitlements).toEqual({
      plan: 'free',
      maxSeats: 1,
      googleSync: false,
      branding: false,
    })
  })

  it('incomplete subscription returns free entitlements', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'incomplete' })

    const entitlements = await getEntitlements(db, orgId)

    expect(entitlements).toEqual({
      plan: 'free',
      maxSeats: 1,
      googleSync: false,
      branding: false,
    })
  })

  it('past_due subscription returns free entitlements', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'past_due' })

    const entitlements = await getEntitlements(db, orgId)

    expect(entitlements).toEqual({
      plan: 'free',
      maxSeats: 1,
      googleSync: false,
      branding: false,
    })
  })

  it('multiple rows, any active/trialing wins', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'canceled' })
    await makeSubscription(db, orgId, { plan: 'premium', status: 'active' })
    await makeSubscription(db, orgId, { plan: 'premium', status: 'incomplete' })

    const entitlements = await getEntitlements(db, orgId)

    expect(entitlements).toEqual({
      plan: 'premium',
      maxSeats: 10,
      googleSync: true,
      branding: true,
    })
  })
})
