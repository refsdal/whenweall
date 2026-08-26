import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import type { NotificationGrid } from '#/lib/notifications'
import { createDb, type Db } from '#/server/db/client'
import { notificationPrefs, notificationSubscriptions } from '#/server/db/schema'
import { resolveRecipients } from '#/server/notifications/recipients'
import {
  addOrgMember,
  makePoll,
  makeSubscription,
  makeUser,
  makeUserWithOrg,
} from '../../../../test/helpers'

const now = () => new Date().toISOString()

/** `makePoll` goes through `createPoll`, which will create the creator's row itself once the
 * lifecycle wiring lands. Clear the table first so each test states its own subscribers. */
async function resetSubscribers(db: Db, pollId: string) {
  await db.delete(notificationSubscriptions)
  return pollId
}

async function subscribe(
  db: Db,
  pollId: string,
  userId: string,
  channels: NotificationGrid | null = null,
  source: 'creator' | 'follow' = 'creator',
) {
  await db.insert(notificationSubscriptions).values({
    scopeType: 'poll',
    scopeId: pollId,
    userId,
    source,
    channels,
    createdAt: now(),
    updatedAt: now(),
  })
}

async function seed() {
  const db = createDb(env.DB)
  const { userId, orgId } = await makeUserWithOrg(db)
  const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })
  await resetSubscribers(db, pollId)
  return {
    db,
    userId,
    orgId,
    pollId,
    scope: { type: 'poll' as const, id: pollId, organizationId: orgId },
  }
}

describe('resolveRecipients', () => {
  it('returns the creator on system defaults', async () => {
    const { db, userId, scope } = await seed()
    await subscribe(db, scope.id, userId)

    const r = await resolveRecipients(db, scope, 'response.created')
    expect(r.email.map((x) => x.userId)).toEqual([userId])
  })

  it('omits an event the user turned off in their defaults', async () => {
    const { db, userId, scope } = await seed()
    await subscribe(db, scope.id, userId)
    await db.insert(notificationPrefs).values({
      userId,
      channels: { 'response.created': { email: false, push: false } },
      createdAt: now(),
      updatedAt: now(),
    })

    const r = await resolveRecipients(db, scope, 'response.created')
    expect(r.email).toEqual([])
  })

  it('lets a per-poll override win over the user default', async () => {
    const { db, userId, scope } = await seed()
    await subscribe(db, scope.id, userId, { 'response.created': { email: true, push: false } })
    await db.insert(notificationPrefs).values({
      userId,
      channels: { 'response.created': { email: false, push: false } },
      createdAt: now(),
      updatedAt: now(),
    })

    const r = await resolveRecipients(db, scope, 'response.created')
    expect(r.email.map((x) => x.userId)).toEqual([userId])
  })

  it('resolves per key, so an override of one event leaves the others alone', async () => {
    const { db, userId, scope } = await seed()
    await subscribe(db, scope.id, userId, { 'response.created': { email: true, push: false } })
    await db.insert(notificationPrefs).values({
      userId,
      channels: { 'comment.created': { email: false, push: false } },
      createdAt: now(),
      updatedAt: now(),
    })

    expect((await resolveRecipients(db, scope, 'comment.created')).email).toEqual([])
    expect((await resolveRecipients(db, scope, 'response.created')).email).toHaveLength(1)
  })

  it('includes a teammate who follows the poll', async () => {
    const { db, userId: owner, orgId, scope } = await seed()
    const { id: mate } = await makeUser(db)
    await addOrgMember(db, orgId, mate)
    await subscribe(db, scope.id, owner)
    await subscribe(db, scope.id, mate, null, 'follow')

    const r = await resolveRecipients(db, scope, 'comment.created')
    expect(r.email.map((x) => x.userId).sort()).toEqual([owner, mate].sort())
  })

  it('excludes a subscriber who is no longer an org member', async () => {
    const { db, userId: owner, scope } = await seed()
    const { userId: outsider } = await makeUserWithOrg(db)
    await subscribe(db, scope.id, owner)
    await subscribe(db, scope.id, outsider, null, 'follow')

    const r = await resolveRecipients(db, scope, 'comment.created')
    expect(r.email.map((x) => x.userId)).toEqual([owner])
  })

  it('suppresses the actor who caused the event', async () => {
    const { db, userId: owner, orgId, scope } = await seed()
    const { id: mate } = await makeUser(db)
    await addOrgMember(db, orgId, mate)
    await subscribe(db, scope.id, owner)
    await subscribe(db, scope.id, mate, null, 'follow')

    const r = await resolveRecipients(db, scope, 'comment.created', { actorUserId: mate })
    expect(r.email.map((x) => x.userId)).toEqual([owner])
  })

  it('resolves no push recipients on a free org and some on premium', async () => {
    const { db, userId, orgId, scope } = await seed()
    await subscribe(db, scope.id, userId)

    const free = await resolveRecipients(db, scope, 'response.created')
    expect(free.push).toEqual([])
    expect(free.email).toHaveLength(1)

    await makeSubscription(db, orgId)
    const premium = await resolveRecipients(db, scope, 'response.created')
    expect(premium.push.map((x) => x.userId)).toEqual([userId])
  })

  it('returns nothing when nobody is subscribed', async () => {
    const { db, scope } = await seed()
    const r = await resolveRecipients(db, scope, 'response.created')
    expect(r).toEqual({ email: [], push: [] })
  })
})
