import { env } from 'cloudflare:workers'
import { runInDurableObject } from 'cloudflare:test'
import { describe, expect, it } from 'vitest'
import { newPollId } from '#/lib/ids'
import { notifyChanged, queueDigest, syncDeadline } from '#/server/notifications/do-client'

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
