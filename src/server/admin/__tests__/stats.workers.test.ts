import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import { getAdminStats } from '#/server/admin/stats'
import {
  makeBookingPage,
  makePoll,
  makeSignupPoll,
  makeSubscription,
  makeUserWithOrg,
} from '../../../../test/helpers'

// The workers pool shares D1 storage across the tests in a file, so every assertion here is a
// delta rather than an absolute count. That is also the more honest shape for a dashboard: what
// matters is that creating a thing moves the right number.
async function delta<T>(read: () => Promise<T>, act: () => Promise<unknown>): Promise<[T, T]> {
  const before = await read()
  await act()
  return [before, await read()]
}

const db = () => createDb(env.DB)

describe('getAdminStats', () => {
  it('counts a poll and a sign-up sheet in separate buckets', async () => {
    const { userId, orgId } = await makeUserWithOrg(db())

    const [before, after] = await delta(
      () => getAdminStats(db()),
      async () => {
        await makePoll(db(), { orgId, createdBy: userId })
        await makeSignupPoll(db(), { orgId, createdBy: userId }, { capacities: [2] })
      },
    )

    expect(after.growth.polls.total - before.growth.polls.total).toBe(1)
    expect(after.growth.signupSheets.total - before.growth.signupSheets.total).toBe(1)
  })

  it('keeps an older row in the total but drops it from last7', async () => {
    const { userId, orgId } = await makeUserWithOrg(db())
    await makePoll(db(), { orgId, createdBy: userId })

    // Ask as if ten days had passed: still counted, no longer recent.
    const future = new Date(Date.now() + 10 * 86_400_000)
    const now = await getAdminStats(db())
    const later = await getAdminStats(db(), future)

    expect(later.growth.polls.total).toBe(now.growth.polls.total)
    expect(later.growth.polls.last7).toBe(0)
    expect(later.growth.polls.last30).toBe(now.growth.polls.last30)
  })

  it('omits soft-deleted content', async () => {
    const { userId, orgId } = await makeUserWithOrg(db())
    const { id } = await makePoll(db(), { orgId, createdBy: userId })

    const before = await getAdminStats(db())
    await db().update(polls).set({ deletedAt: new Date().toISOString() }).where(eq(polls.id, id))
    const after = await getAdminStats(db())

    expect(before.growth.polls.total - after.growth.polls.total).toBe(1)
  })

  // The dashboard must agree with the entitlement gate about who is Premium. 'trialing' counts as
  // active in entitlements.ts, so it has to count here too — hence `isActivePremium` is reused
  // rather than re-derived.
  it('counts a trialing subscription as premium, like the entitlement gate does', async () => {
    const { orgId } = await makeUserWithOrg(db())

    const [before, after] = await delta(
      () => getAdminStats(db()),
      () => makeSubscription(db(), orgId, { status: 'trialing' }),
    )

    expect(after.revenue.premiumOrgs - before.revenue.premiumOrgs).toBe(1)
    expect(after.revenue.activeSubscriptions - before.revenue.activeSubscriptions).toBe(1)
  })

  it('ignores a cancelled subscription', async () => {
    const { orgId } = await makeUserWithOrg(db())

    const [before, after] = await delta(
      () => getAdminStats(db()),
      () => makeSubscription(db(), orgId, { status: 'canceled' }),
    )

    expect(after.revenue.premiumOrgs).toBe(before.revenue.premiumOrgs)
    expect(after.revenue.mrrMinor).toBe(before.revenue.mrrMinor)
  })

  it('adds 6000 minor units of MRR for a monthly subscription', async () => {
    const { orgId } = await makeUserWithOrg(db())

    const [before, after] = await delta(
      () => getAdminStats(db()),
      () => makeSubscription(db(), orgId, { status: 'active' }),
    )

    expect(after.revenue.mrrMinor - before.revenue.mrrMinor).toBe(6000)
  })

  it('counts booking pages', async () => {
    const { userId, orgId } = await makeUserWithOrg(db())

    const [before, after] = await delta(
      () => getAdminStats(db()),
      () => makeBookingPage(db(), { orgId, createdBy: userId }),
    )

    expect(after.growth.bookingPages.total - before.growth.bookingPages.total).toBe(1)
  })
})
