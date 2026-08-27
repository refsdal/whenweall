import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb, type Db } from '#/server/db/client'
import { notificationSubscriptions } from '#/server/db/schema'
import { pollRoom } from '#/server/notifications/do-client'
import { emitPollEvent } from '#/server/notifications/emit'
import { setScopeChannels } from '#/server/notifications/subscriptions'
import { makePoll, makeUserWithOrg } from '../../../../test/helpers'

async function seed() {
  const db: Db = createDb(env.DB)
  const { userId, orgId } = await makeUserWithOrg(db)
  const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })
  return { db, userId, orgId, pollId }
}

describe('emitPollEvent', () => {
  it('queues an activity event onto the poll room digest', async () => {
    const { pollId } = await seed()

    await emitPollEvent(pollId, 'response.updated', { actorName: 'Ada', actorUserId: null })

    const items = await pollRoom(pollId).peekDigestItems()
    expect(items.map((i) => i.event)).toEqual(['response.updated'])
    expect(items[0]!.name).toBe('Ada')
  })

  it('does not queue a lifecycle event onto the digest', async () => {
    const { pollId } = await seed()

    await emitPollEvent(pollId, 'poll.closed', { actorUserId: null })

    expect(await pollRoom(pollId).peekDigestItems()).toEqual([])
  })

  it('never throws when the poll does not exist', async () => {
    await expect(
      emitPollEvent('missing-poll', 'response.created', { actorName: 'Ada', actorUserId: null }),
    ).resolves.toBeUndefined()
  })

  it('skips the digest entirely when nobody is subscribed', async () => {
    const { db, pollId } = await seed()
    await db.delete(notificationSubscriptions).where(eq(notificationSubscriptions.scopeId, pollId))

    await emitPollEvent(pollId, 'response.created', { actorName: 'Ada', actorUserId: null })

    expect(await pollRoom(pollId).peekDigestItems()).toEqual([])
  })

  it('skips the digest when the only subscriber has the event switched off', async () => {
    const { db, userId, pollId } = await seed()
    await setScopeChannels(db, { type: 'poll', id: pollId }, userId, {
      'response.created': { email: false, push: false },
    })

    await emitPollEvent(pollId, 'response.created', { actorName: 'Ada', actorUserId: null })

    expect(await pollRoom(pollId).peekDigestItems()).toEqual([])
  })

  it('skips the digest when the only subscriber is the actor', async () => {
    const { userId, pollId } = await seed()

    await emitPollEvent(pollId, 'response.updated', { actorName: 'Self', actorUserId: userId })

    expect(await pollRoom(pollId).peekDigestItems()).toEqual([])
  })

  it('carries the actor id through so the alarm can suppress per item', async () => {
    const { pollId } = await seed()

    await emitPollEvent(pollId, 'comment.created', {
      actorName: 'Guest',
      actorUserId: 'someone-else',
    })

    const items = await pollRoom(pollId).peekDigestItems()
    expect(items[0]!.actorUserId).toBe('someone-else')
  })
})
