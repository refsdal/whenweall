import { env } from 'cloudflare:workers'
import { describe, it, expect, beforeEach } from 'vitest'
import { createDb } from '#/server/db/client'
import { getEntitlements, getSeatsUsed } from '../entitlements'
import {
  addOrgMember,
  makeInvitation,
  makeOrg,
  makeUser,
  makeSubscription,
} from '../../../../test/helpers'
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

describe('getSeatsUsed', () => {
  let db: Db

  beforeEach(() => {
    db = createDb(env.DB)
  })

  it('counts only the owner when there are no other members or invitations', async () => {
    const { id: userId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)

    expect(await getSeatsUsed(db, orgId)).toBe(1)
  })

  it('counts members plus pending invitations, mirroring the invite seat gate', async () => {
    const { id: ownerId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, ownerId)

    for (let i = 0; i < 2; i++) {
      const { id: memberId } = await makeUser(db)
      await addOrgMember(db, orgId, memberId)
    }
    for (let i = 0; i < 3; i++) {
      await makeInvitation(db, orgId, ownerId)
    }

    // owner + 2 members + 3 pending invitations = 6
    expect(await getSeatsUsed(db, orgId)).toBe(6)
  })

  it('does not count accepted or canceled invitations', async () => {
    const { id: ownerId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, ownerId)

    await makeInvitation(db, orgId, ownerId, { status: 'accepted' })
    await makeInvitation(db, orgId, ownerId, { status: 'canceled' })
    await makeInvitation(db, orgId, ownerId, { status: 'rejected' })

    // owner only — none of the non-pending invitations are occupied seats
    expect(await getSeatsUsed(db, orgId)).toBe(1)
  })

  it('is scoped to the given org — another org’s members and invitations do not leak in', async () => {
    const { id: ownerId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, ownerId)
    const { id: otherOwnerId } = await makeUser(db)
    const { id: otherOrgId } = await makeOrg(db, otherOwnerId)
    await makeInvitation(db, otherOrgId, otherOwnerId)

    expect(await getSeatsUsed(db, orgId)).toBe(1)
  })

  it('does not count an expired pending invitation (Better-Auth refuses to accept it anyway, so an unfiltered count would hold that seat forever)', async () => {
    const { id: ownerId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, ownerId)
    await makeInvitation(db, orgId, ownerId, { expiresAt: new Date(Date.now() - 1000) })

    // owner only — the expired invitation is not an occupied seat
    expect(await getSeatsUsed(db, orgId)).toBe(1)
  })
})
