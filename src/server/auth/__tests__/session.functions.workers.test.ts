import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { buildClientSession } from '#/server/auth/session.functions'
import type { Session } from '#/server/auth/auth'
import { makeOrg, makeUser } from '../../../../test/helpers'

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
  })

  it('returns org: null when the active org no longer has a membership row for this user', async () => {
    const db = createDb(env.DB)
    const { id: userId, email } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: orgId } = await makeOrg(db, otherId) // userId is not a member of this org

    const result = await buildClientSession(
      db,
      fakeSession({ id: userId, name: 'Test User', email }, orgId),
    )

    expect(result?.org).toBeNull()
  })
})
