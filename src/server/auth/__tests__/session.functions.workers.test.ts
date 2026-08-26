import { env } from 'cloudflare:workers'
import { and, eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { member } from '#/server/db/schema'
import { buildClientSession } from '#/server/auth/session.functions'
import type { Session } from '#/server/auth/auth'
import { FREE_ENTITLEMENTS } from '#/server/billing/entitlements'
import { makeOrg, makeSubscription, makeUser } from '../../../../test/helpers'

/** A minimal stand-in for Better-Auth's `Session` shape — just the fields `buildClientSession`
 * actually reads (`user.{id,name,email,image}`, `user.locale` as an additional field, and
 * `session.activeOrganizationId`). */
function fakeSession(
  user: { id: string; name: string; email: string; image?: string | null; locale?: string | null },
  activeOrganizationId: string | null,
): Session {
  return {
    user: { ...user, image: user.image ?? null },
    session: { activeOrganizationId },
  } as unknown as Session
}

describe('buildClientSession', () => {
  it('returns null for no session', async () => {
    const db = createDb(env.DB)
    await expect(buildClientSession(db, null)).resolves.toBeNull()
  })

  it('returns org: { id, slug, name, role } for a user with an active org — client code trusts this shape', async () => {
    const db = createDb(env.DB)
    const { id: userId, email } = await makeUser(db, { name: 'Ada', locale: 'nb' })
    const { id: orgId, slug } = await makeOrg(db, userId, { name: 'Ada Org', role: 'admin' })

    const result = await buildClientSession(
      db,
      fakeSession({ id: userId, name: 'Ada', email, locale: 'nb' }, orgId),
    )

    expect(result).toEqual({
      user: { id: userId, name: 'Ada', email, image: null, locale: 'nb' },
      org: { id: orgId, slug, name: 'Ada Org', role: 'admin' },
      entitlements: FREE_ENTITLEMENTS,
    })
  })

  it('includes premium entitlements once the org has an active premium subscription', async () => {
    const db = createDb(env.DB)
    const { id: userId, email } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, userId)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'active' })

    const result = await buildClientSession(
      db,
      fakeSession({ id: userId, name: 'Test User', email }, orgId),
    )

    expect(result?.entitlements).toEqual({
      plan: 'premium',
      maxSeats: 10,
      googleSync: true,
      branding: true,
    })
  })

  it('returns org: null when the session has no active organization', async () => {
    const db = createDb(env.DB)
    const { id: userId, email } = await makeUser(db)

    const result = await buildClientSession(
      db,
      fakeSession({ id: userId, name: 'Test User', email }, null),
    )

    expect(result?.org).toBeNull()
    expect(result?.entitlements).toEqual(FREE_ENTITLEMENTS)
  })

  it('falls back to a lazily-created personal org when the active org no longer has a membership row for this user (dangling activeOrganizationId)', async () => {
    const db = createDb(env.DB)
    const { id: userId, email } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, otherId) // userId is not a member of this org

    const result = await buildClientSession(
      db,
      fakeSession({ id: userId, name: 'Test User', email }, orgId),
    )

    expect(result?.org?.id).not.toBe(orgId)
    expect(result?.org).toMatchObject({ name: 'Test User', role: 'owner' })
  })

  it('falls back to the oldest remaining membership when the active org id is dangling but another membership exists', async () => {
    const db = createDb(env.DB)
    const { id: userId, email } = await makeUser(db)
    const { id: oldestOrgId, slug: oldestSlug } = await makeOrg(db, userId, { name: 'Oldest Org' })
    const { id: staleOrgId } = await makeOrg(db, userId, { name: 'Stale Org' })
    // Force explicit, unambiguous createdAt ordering (independent of real-clock timing between
    // the two makeOrg calls above).
    await db
      .update(member)
      .set({ createdAt: new Date('2020-01-01') })
      .where(and(eq(member.organizationId, oldestOrgId), eq(member.userId, userId)))
    await db
      .update(member)
      .set({ createdAt: new Date('2020-06-01') })
      .where(and(eq(member.organizationId, staleOrgId), eq(member.userId, userId)))
    // The session's activeOrganizationId points at staleOrgId, but that membership row is gone.
    await db
      .delete(member)
      .where(and(eq(member.organizationId, staleOrgId), eq(member.userId, userId)))

    const result = await buildClientSession(
      db,
      fakeSession({ id: userId, name: 'Test User', email }, staleOrgId),
    )

    expect(result?.org).toEqual({
      id: oldestOrgId,
      slug: oldestSlug,
      name: 'Oldest Org',
      role: 'owner',
    })
  })
})
