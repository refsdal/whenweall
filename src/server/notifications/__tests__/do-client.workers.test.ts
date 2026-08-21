import { env } from 'cloudflare:workers'
import { runInDurableObject } from 'cloudflare:test'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { newPollId } from '#/lib/ids'
import { getPollView } from '#/server/polls/service'
import {
  claimViaRoom,
  notifyChanged,
  queueDigest,
  syncDeadline,
  unclaimViaRoom,
} from '#/server/notifications/do-client'
import { makeSignupPoll, makeUser } from '../../../../test/helpers'

function stubFor(pollId: string) {
  return env.POLL_ROOM.getByName(pollId)
}

describe('do-client', () => {
  it('notifyChanged resolves without throwing', async () => {
    await expect(notifyChanged(newPollId(), 'poll')).resolves.toBeUndefined()
  })

  it('queueDigest resolves without throwing', async () => {
    const pollId = newPollId()
    const item = { kind: 'vote' as const, name: 'Ada', at: new Date().toISOString() }

    await expect(queueDigest(pollId, item)).resolves.toBeUndefined()

    const items = await runInDurableObject(stubFor(pollId), (_instance, state) =>
      state.storage.get<unknown[]>('digest:items'),
    )
    expect(items).toHaveLength(1)
  })

  it('syncDeadline sets the alarm on the poll room durable object', async () => {
    const pollId = newPollId()
    const future = new Date(Date.now() + 60_000).toISOString()

    await syncDeadline(pollId, future)

    const alarm = await runInDurableObject(stubFor(pollId), (_instance, state) =>
      state.storage.getAlarm(),
    )
    expect(alarm).not.toBeNull()
  })
})

describe('claimViaRoom / unclaimViaRoom', () => {
  it('claimViaRoom resolves with the claim result on success', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [null] })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const slot = view!.options[0]!

    const result = await claimViaRoom(pollId, slot.id, { name: 'Alice', userId: null })
    expect(result.created).toBe(true)
    expect(result.claimedOptionIds).toEqual([slot.id])
  })

  it('propagates a business error instead of swallowing it (NOT best-effort)', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [1] })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const slot = view!.options[0]!

    await claimViaRoom(pollId, slot.id, { name: 'Alice', userId: null })
    await expect(claimViaRoom(pollId, slot.id, { name: 'Bob', userId: null })).rejects.toBeDefined()
  })

  it('unclaimViaRoom resolves with the remaining claimed option ids', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [null] })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const slot = view!.options[0]!
    const claim = await claimViaRoom(pollId, slot.id, { name: 'Alice', userId: null })

    const result = await unclaimViaRoom(pollId, slot.id, claim.participantId)
    expect(result.remainingOptionIds).toEqual([])
  })
})
