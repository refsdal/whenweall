import { env } from 'cloudflare:workers'
import { runDurableObjectAlarm, runInDurableObject } from 'cloudflare:test'
import { describe, expect, it } from 'vitest'
import { FLUSH_INTERVAL_MS, type StatsRoom } from '#/do/StatsRoom'
import type { UsageStats } from '#/do/stats-protocol'
import { createDb } from '#/server/db/client'
import { finalizePoll } from '#/server/polls/service'
import { makeParticipant, makePoll, makeUserWithOrg } from '../../../test/helpers'

let roomSeq = 0
/** A fresh instance per test — the production name is a constant, so tests must not share it. */
function freshRoom() {
  roomSeq += 1
  return env.STATS_ROOM.getByName(`test-${roomSeq}-${Date.now()}`)
}

async function storage<T>(stub: ReturnType<typeof freshRoom>, key: string): Promise<T | undefined> {
  return runInDurableObject(stub, (_i, state) => state.storage.get<T>(key))
}

describe('StatsRoom counters', () => {
  it('accumulates increments and reflects them before a flush', async () => {
    const stub = freshRoom()

    await stub.recordPollCreated()
    await stub.recordPollCreated()
    await stub.recordResponses(['yes', 'no', 'yes', 'ifneedbe'])

    const stats = await stub.read()
    expect(stats.pollsCreated).toBe(2)
    expect(stats.responsesYes).toBe(2)
    expect(stats.responsesNo).toBe(1)
    expect(stats.responsesIfNeedBe).toBe(1)
  })

  it('persists the delta to storage on the flush alarm', async () => {
    const stub = freshRoom()
    await stub.recordPollCreated()
    await stub.recordResponses(['yes'])

    await runDurableObjectAlarm(stub)

    const stored = await storage<UsageStats>(stub, 'counters')
    expect(stored?.pollsCreated).toBe(1)
    expect(stored?.responsesYes).toBe(1)
    // The read is unchanged by flushing — it is the same number, just now durable.
    expect((await stub.read()).pollsCreated).toBe(1)
  })

  it('does not double-count across two flushes', async () => {
    const stub = freshRoom()
    await stub.recordResponses(['yes', 'yes'])
    await runDurableObjectAlarm(stub)
    await stub.recordResponses(['yes'])
    await runDurableObjectAlarm(stub)

    expect((await stub.read()).responsesYes).toBe(3)
  })

  it('arms the flush alarm once rather than pushing it out on every increment', async () => {
    const stub = freshRoom()
    const before = Date.now()

    await stub.recordPollCreated()
    const first = await runInDurableObject(stub, (_i, state) => state.storage.getAlarm())
    await stub.recordPollCreated()
    const second = await runInDurableObject(stub, (_i, state) => state.storage.getAlarm())

    expect(first).not.toBeNull()
    expect(second).toBe(first)
    expect(first!).toBeGreaterThanOrEqual(before)
    expect(first!).toBeLessThanOrEqual(before + FLUSH_INTERVAL_MS + 2000)
  })

  it('ignores an empty answer list', async () => {
    const stub = freshRoom()
    await stub.recordResponses([])
    expect((await stub.read()).responsesYes).toBe(0)
    expect(await runInDurableObject(stub, (_i, state) => state.storage.getAlarm())).toBeNull()
  })
})

describe('StatsRoom seeding', () => {
  it('seeds from existing polls and votes on first read', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })
    // makePoll creates two datetime options; answer both.
    const options = await db.query.pollOptions.findMany()
    const optionIds = options.filter((o) => o.pollId === pollId).map((o) => o.id)
    await makeParticipant(db, pollId, 'Ada', {
      [optionIds[0]!]: 'yes',
      [optionIds[1]!]: 'no',
    })

    const stub = freshRoom()
    const stats = await stub.read()

    // Other tests in this worker share the D1 database, so assert floors rather than exact totals.
    expect(stats.pollsCreated).toBeGreaterThanOrEqual(1)
    expect(stats.responsesYes).toBeGreaterThanOrEqual(1)
    expect(stats.responsesNo).toBeGreaterThanOrEqual(1)
  })

  it('seeds exactly once, so a later read does not re-run the aggregate', async () => {
    const stub = freshRoom()
    const first = await stub.read()

    // A new poll created after seeding must not appear until something increments — proving the
    // second read used the stored counters rather than re-querying D1.
    const db = createDb(env.DB)
    const { userId, orgId } = await makeUserWithOrg(db)
    await makePoll(db, { orgId, createdBy: userId })

    const second = await stub.read()
    expect(second.pollsCreated).toBe(first.pollsCreated)
  })

  it('adds live increments on top of the seeded floor', async () => {
    const stub = freshRoom()
    const seeded = await stub.read()

    await stub.recordPollCreated()
    await stub.recordResponses(['ifneedbe'])

    const after = await stub.read()
    expect(after.pollsCreated).toBe(seeded.pollsCreated + 1)
    expect(after.responsesIfNeedBe).toBe(seeded.responsesIfNeedBe + 1)
  })
})

describe('StatsRoom websocket', () => {
  it('rejects a non-websocket request', async () => {
    const stub = freshRoom()
    const res = await stub.fetch(new Request('https://stats/'))
    expect(res.status).toBe(426)
  })

  it('greets a new socket with the current numbers', async () => {
    const stub = freshRoom()
    await stub.recordPollCreated()

    const res = await stub.fetch(
      new Request('https://stats/', { headers: { Upgrade: 'websocket' } }),
    )
    expect(res.status).toBe(101)

    const ws = res.webSocket!
    ws.accept()
    const message = await new Promise<string>((resolve) => {
      ws.addEventListener('message', (e) => resolve(e.data as string), { once: true })
    })

    const parsed = JSON.parse(message) as { type: string; stats: UsageStats }
    expect(parsed.type).toBe('stats')
    expect(parsed.stats.pollsCreated).toBeGreaterThanOrEqual(1)
    ws.close()
  })
})

describe('StatsRoom finalized counter', () => {
  it('counts a decided poll separately from a created one', async () => {
    const stub = freshRoom()
    const before = await stub.read()

    await stub.recordPollCreated()
    await stub.recordPollFinalized()

    const after = await stub.read()
    expect(after.pollsCreated).toBe(before.pollsCreated + 1)
    expect(after.pollsFinalized).toBe(before.pollsFinalized + 1)
  })

  it('persists the finalized delta across a flush', async () => {
    const stub = freshRoom()
    await stub.recordPollFinalized()
    await stub.recordPollFinalized()
    await runDurableObjectAlarm(stub)

    const stored = await storage<UsageStats>(stub, 'counters')
    expect(stored?.pollsFinalized).toBe(2)
    expect((await stub.read()).pollsFinalized).toBe(2)
  })

  it('seeds the finalized count from polls already decided', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })
    const options = await db.query.pollOptions.findMany()
    const optionId = options.find((o) => o.pollId === pollId)!.id
    await finalizePoll(db, pollId, { id: orgId, role: 'owner' }, userId, optionId)

    const stub = freshRoom()
    // Shared D1 across the file, so assert a floor rather than an exact total.
    expect((await stub.read()).pollsFinalized).toBeGreaterThanOrEqual(1)
  })
})

/** Keeps the unused-type import honest if the suite is trimmed. */
export type _StatsRoom = StatsRoom
