import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import {
  readUsageStats,
  recordPollCreated,
  recordResponses,
  statsRoom,
  STATS_ROOM_NAME,
} from '#/server/stats/stats-client'

describe('stats-client', () => {
  it('addresses the one global room', () => {
    expect(STATS_ROOM_NAME).toBe('global')
    expect(statsRoom()).toBeDefined()
  })

  it('records a poll and reads it back through the client', async () => {
    const before = await readUsageStats()
    await recordPollCreated()
    const after = await readUsageStats()

    expect(after.pollsCreated).toBe(before.pollsCreated + 1)
  })

  it('records answers by kind', async () => {
    const before = await readUsageStats()
    await recordResponses(['yes', 'yes', 'no'])
    const after = await readUsageStats()

    expect(after.responsesYes).toBe(before.responsesYes + 2)
    expect(after.responsesNo).toBe(before.responsesNo + 1)
    expect(after.responsesIfNeedBe).toBe(before.responsesIfNeedBe)
  })

  it('is a no-op for an empty answer list', async () => {
    const before = await readUsageStats()
    await recordResponses([])
    expect(await readUsageStats()).toEqual(before)
  })

  it('never throws when the durable object is unreachable', async () => {
    // Swap the binding for one whose stub rejects, proving the best-effort contract holds rather
    // than assuming it from the try/catch being present.
    const original = env.STATS_ROOM
    const broken = {
      getByName: () => ({
        recordPollCreated: () => Promise.reject(new Error('boom')),
        recordResponses: () => Promise.reject(new Error('boom')),
        read: () => Promise.reject(new Error('boom')),
      }),
    }

    Object.defineProperty(env, 'STATS_ROOM', { value: broken, configurable: true })
    try {
      await expect(recordPollCreated()).resolves.toBeUndefined()
      await expect(recordResponses(['yes'])).resolves.toBeUndefined()
      // The read falls back to zeroes rather than throwing — the landing page must still render.
      await expect(readUsageStats()).resolves.toEqual({
        pollsFinalized: 0,
        pollsCreated: 0,
        responsesYes: 0,
        responsesIfNeedBe: 0,
        responsesNo: 0,
      })
    } finally {
      Object.defineProperty(env, 'STATS_ROOM', { value: original, configurable: true })
    }
  })
})
