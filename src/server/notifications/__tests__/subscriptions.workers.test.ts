import { env } from 'cloudflare:workers'
import { and, eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb, type Db } from '#/server/db/client'
import { notificationSubscriptions } from '#/server/db/schema'
import {
  deleteScopeSubscriptions,
  ensureCreatorSubscription,
  followScope,
  setScopeChannels,
  unfollowScope,
} from '#/server/notifications/subscriptions'
import { addOrgMember, makePoll, makeUser, makeUserWithOrg } from '../../../../test/helpers'

async function rowsFor(db: Db, pollId: string) {
  return db.query.notificationSubscriptions.findMany({
    where: eq(notificationSubscriptions.scopeId, pollId),
  })
}

async function seed() {
  const db = createDb(env.DB)
  const { userId, orgId } = await makeUserWithOrg(db)
  const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })
  await db.delete(notificationSubscriptions)
  return { db, userId, orgId, pollId, scope: { type: 'poll' as const, id: pollId } }
}

describe('subscription lifecycle', () => {
  it('creates the creator row once and is idempotent', async () => {
    const { db, userId, scope } = await seed()

    await ensureCreatorSubscription(db, scope, userId)
    await ensureCreatorSubscription(db, scope, userId)

    const rows = await rowsFor(db, scope.id)
    expect(rows).toHaveLength(1)
    expect(rows[0]!.source).toBe('creator')
    expect(rows[0]!.channels ?? null).toBeNull()
  })

  it('does nothing when the creator is null', async () => {
    const { db, scope } = await seed()
    await ensureCreatorSubscription(db, scope, null)
    expect(await rowsFor(db, scope.id)).toHaveLength(0)
  })

  it('follows and unfollows a teammate without touching the creator row', async () => {
    const { db, userId: owner, orgId, scope } = await seed()
    const { id: mate } = await makeUser(db)
    await addOrgMember(db, orgId, mate)

    await ensureCreatorSubscription(db, scope, owner)
    await followScope(db, scope, mate)
    expect(await rowsFor(db, scope.id)).toHaveLength(2)

    await unfollowScope(db, scope, mate)
    expect((await rowsFor(db, scope.id)).map((r) => r.userId)).toEqual([owner])
  })

  it('does not reset an existing override when the same user is re-added', async () => {
    const { db, userId, scope } = await seed()
    await ensureCreatorSubscription(db, scope, userId)
    await setScopeChannels(db, scope, userId, { 'comment.created': { email: false, push: false } })

    await followScope(db, scope, userId)

    const rows = await rowsFor(db, scope.id)
    expect(rows).toHaveLength(1)
    expect(rows[0]!.channels?.['comment.created']).toEqual({ email: false, push: false })
    expect(rows[0]!.source).toBe('creator')
  })

  it('writes and clears a per-scope override', async () => {
    const { db, userId, scope } = await seed()
    await ensureCreatorSubscription(db, scope, userId)

    await setScopeChannels(db, scope, userId, { 'comment.created': { email: false, push: false } })
    let row = await db.query.notificationSubscriptions.findFirst({
      where: and(
        eq(notificationSubscriptions.scopeId, scope.id),
        eq(notificationSubscriptions.userId, userId),
      ),
    })
    expect(row?.channels?.['comment.created']).toEqual({ email: false, push: false })

    await setScopeChannels(db, scope, userId, null)
    row = await db.query.notificationSubscriptions.findFirst({
      where: and(
        eq(notificationSubscriptions.scopeId, scope.id),
        eq(notificationSubscriptions.userId, userId),
      ),
    })
    expect(row?.channels ?? null).toBeNull()
  })

  it('deletes every subscription for a scope', async () => {
    const { db, userId: owner, orgId, scope } = await seed()
    const { id: mate } = await makeUser(db)
    await addOrgMember(db, orgId, mate)

    await ensureCreatorSubscription(db, scope, owner)
    await followScope(db, scope, mate)
    await deleteScopeSubscriptions(db, scope)

    expect(await rowsFor(db, scope.id)).toHaveLength(0)
  })

  it('scopes deletion to one scope type', async () => {
    const { db, userId, scope } = await seed()
    await ensureCreatorSubscription(db, scope, userId)
    await ensureCreatorSubscription(db, { type: 'booking_page', id: scope.id }, userId)

    await deleteScopeSubscriptions(db, scope)

    const rows = await rowsFor(db, scope.id)
    expect(rows.map((r) => r.scopeType)).toEqual(['booking_page'])
  })
})
