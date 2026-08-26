import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { authorizeSubscriptionReference } from '#/server/billing/stripe'
import { addOrgMember, makeOrg, makeUser } from '../../../../test/helpers'

describe('authorizeSubscriptionReference', () => {
  it('allows the org owner', async () => {
    const db = createDb(env.DB)
    const owner = await makeUser(db)
    const org = await makeOrg(db, owner.id)
    expect(
      await authorizeSubscriptionReference(db, { userId: owner.id, referenceId: org.id }),
    ).toBe(true)
  })

  it('blocks an admin', async () => {
    const db = createDb(env.DB)
    const owner = await makeUser(db)
    const org = await makeOrg(db, owner.id)
    const admin = await makeUser(db)
    await addOrgMember(db, org.id, admin.id, 'admin')
    expect(
      await authorizeSubscriptionReference(db, { userId: admin.id, referenceId: org.id }),
    ).toBe(false)
  })

  it('blocks a plain member', async () => {
    const db = createDb(env.DB)
    const owner = await makeUser(db)
    const org = await makeOrg(db, owner.id)
    const member = await makeUser(db)
    await addOrgMember(db, org.id, member.id, 'member')
    expect(
      await authorizeSubscriptionReference(db, { userId: member.id, referenceId: org.id }),
    ).toBe(false)
  })

  it('blocks a non-member', async () => {
    const db = createDb(env.DB)
    const owner = await makeUser(db)
    const org = await makeOrg(db, owner.id)
    const outsider = await makeUser(db)
    expect(
      await authorizeSubscriptionReference(db, { userId: outsider.id, referenceId: org.id }),
    ).toBe(false)
  })
})
