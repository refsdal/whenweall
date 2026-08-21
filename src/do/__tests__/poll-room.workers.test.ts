import { env } from 'cloudflare:workers'
import { runDurableObjectAlarm, runInDurableObject } from 'cloudflare:test'
import { eq } from 'drizzle-orm'
import { beforeEach, describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import type { MailMessage } from '#/server/mailer/mailer'
import { DIGEST_DELAY_MS, MAX_RETRIES, PollRoom, RETRY_DELAY_MS } from '#/do/PollRoom'
import type { PollEvent } from '#/do/protocol'
import { makePoll, makeUser } from '../../../test/helpers'

// `vi.mock` cannot reach code running inside a Durable Object's own module graph in
// @cloudflare/vitest-plugin 1.0 — `alarm()`/RPC methods load via a separate `importModule()` path
// that does not share the test file's mock registry. `PollRoom.mailer` is an overridable instance
// field (default: the real `sendMail`) that tests set directly through `runInDurableObject`.

const ALARM_TOLERANCE_MS = 2000

function stubFor(pollId: string) {
  return env.POLL_ROOM.getByName(pollId)
}

async function getAlarm(stub: ReturnType<typeof stubFor>): Promise<number | null> {
  return runInDurableObject(stub, (_instance, state) => state.storage.getAlarm())
}

async function getStorage<T>(
  stub: ReturnType<typeof stubFor>,
  key: string,
): Promise<T | undefined> {
  return runInDurableObject(stub, (_instance, state) => state.storage.get<T>(key))
}

// Alarms are scheduled for the real future (as production requires), so `runDurableObjectAlarm`
// force-fires the DO's real alarm early without making our own `digest:at <= now` /
// `deadline:at <= now` due-checks true. Overwrite the tracking key directly to simulate that the
// delay has elapsed, without racing the miniflare scheduler by ever scheduling an alarm in the past.
async function forceDue(stub: ReturnType<typeof stubFor>, key: string): Promise<void> {
  await runInDurableObject(stub, async (_instance, state) => {
    await state.storage.put(key, Date.now() - 1)
  })
}

// Installs a fake mailer on the live DO instance. The override lives on the instance itself, so it
// persists for the lifetime of the test as long as the object isn't evicted — reapply right before
// each `runDurableObjectAlarm` call to be safe.
async function installMailer(
  stub: ReturnType<typeof stubFor>,
  impl: (env: unknown, msg: MailMessage) => Promise<boolean>,
): Promise<void> {
  await runInDurableObject(stub, (instance: PollRoom) => {
    instance.mailer = impl as PollRoom['mailer']
  })
}

let sent: MailMessage[]

beforeEach(() => {
  sent = []
})

function recordingMailer(result: boolean) {
  return async (_env: unknown, msg: MailMessage): Promise<boolean> => {
    sent.push(msg)
    return result
  }
}

async function throwingMailer(): Promise<boolean> {
  throw new Error('mailer boom')
}

describe('enqueueDigest', () => {
  it('schedules the digest alarm ~10 minutes out and accumulates items', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const stub = stubFor(pollId)
    const before = Date.now()

    await stub.enqueueDigest(pollId, { kind: 'vote', name: 'Ada', at: new Date().toISOString() })
    await stub.enqueueDigest(pollId, { kind: 'comment', name: 'Bob', at: new Date().toISOString() })

    const alarmAt = await getAlarm(stub)
    expect(alarmAt).not.toBeNull()
    expect(alarmAt!).toBeGreaterThanOrEqual(before + DIGEST_DELAY_MS - ALARM_TOLERANCE_MS)
    expect(alarmAt!).toBeLessThanOrEqual(before + DIGEST_DELAY_MS + ALARM_TOLERANCE_MS)

    const items = await getStorage(stub, 'digest:items')
    expect(items).toHaveLength(2)
  })
})

describe('alarm — digest', () => {
  it('sends exactly one digest mail to the owner and clears storage', async () => {
    const db = createDb(env.DB)
    const { id: ownerId, email: ownerEmail } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const stub = stubFor(pollId)

    await stub.enqueueDigest(pollId, { kind: 'vote', name: 'Ada', at: new Date().toISOString() })
    await stub.enqueueDigest(pollId, { kind: 'comment', name: 'Bob', at: new Date().toISOString() })
    await forceDue(stub, 'digest:at')
    await installMailer(stub, recordingMailer(true))

    const ran = await runDurableObjectAlarm(stub)
    expect(ran).toBe(true)

    expect(sent).toHaveLength(1)
    expect(sent[0]!.to).toBe(ownerEmail)
    // newComments is only reflected as a count in the digest template, not by name.
    expect(sent[0]!.html).toContain('Ada')

    expect(await getStorage(stub, 'digest:items')).toBeUndefined()
    expect(await getStorage(stub, 'digest:at')).toBeUndefined()
    expect(await getStorage(stub, 'retry:count')).toBeUndefined()
    expect(await getAlarm(stub)).toBeNull()
  })

  it('sends no mail and clears storage when nothing matches the owner notification flags', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    await db.update(polls).set({ notifyOnVote: false }).where(eq(polls.id, pollId))
    const stub = stubFor(pollId)

    await stub.enqueueDigest(pollId, { kind: 'vote', name: 'Ada', at: new Date().toISOString() })
    await forceDue(stub, 'digest:at')
    await installMailer(stub, recordingMailer(true))

    const ran = await runDurableObjectAlarm(stub)
    expect(ran).toBe(true)

    expect(sent).toHaveLength(0)
    expect(await getStorage(stub, 'digest:items')).toBeUndefined()
    expect(await getStorage(stub, 'digest:at')).toBeUndefined()
    expect(await getAlarm(stub)).toBeNull()
  })

  it('retries on mail failure and drops the digest after MAX_RETRIES failures', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const stub = stubFor(pollId)

    await stub.enqueueDigest(pollId, { kind: 'vote', name: 'Ada', at: new Date().toISOString() })
    await forceDue(stub, 'digest:at')
    await installMailer(stub, recordingMailer(false))

    const before = Date.now()
    await runDurableObjectAlarm(stub)
    expect(await getStorage(stub, 'retry:count')).toBe(1)
    expect(await getStorage(stub, 'digest:items')).toHaveLength(1)
    const alarmAt = await getAlarm(stub)
    expect(alarmAt!).toBeGreaterThanOrEqual(before + RETRY_DELAY_MS - ALARM_TOLERANCE_MS)
    expect(alarmAt!).toBeLessThanOrEqual(before + RETRY_DELAY_MS + ALARM_TOLERANCE_MS)

    // Force the retry due "now" instead of waiting for real time to pass.
    for (let attempt = 2; attempt <= MAX_RETRIES; attempt += 1) {
      await forceDue(stub, 'digest:at')
      await installMailer(stub, recordingMailer(false))
      await runDurableObjectAlarm(stub)
      if (attempt < MAX_RETRIES) {
        expect(await getStorage(stub, 'retry:count')).toBe(attempt)
      }
    }

    expect(sent).toHaveLength(MAX_RETRIES)
    expect(await getStorage(stub, 'digest:items')).toBeUndefined()
    expect(await getStorage(stub, 'digest:at')).toBeUndefined()
    expect(await getStorage(stub, 'retry:count')).toBeUndefined()
    expect(await getAlarm(stub)).toBeNull()
  })
})

describe('alarm — deadline', () => {
  it('closes an expired poll, emails the owner, and clears the deadline alarm', async () => {
    const db = createDb(env.DB)
    const { id: ownerId, email: ownerEmail } = await makeUser(db)
    // D1's deadlineAt is what closeExpiredPoll checks — keep it genuinely in the past.
    const past = new Date(Date.now() - 60_000).toISOString()
    const { id: pollId } = await makePoll(db, ownerId, { deadlineAt: past })
    const stub = stubFor(pollId)

    // Schedule the DO's own alarm for the future so miniflare doesn't auto-fire it before we're
    // ready, then force the tracking key due directly (see forceDue for why).
    const future = new Date(Date.now() + 60_000).toISOString()
    await stub.syncDeadline(pollId, future)
    expect(await getAlarm(stub)).not.toBeNull()
    await forceDue(stub, 'deadline:at')
    await installMailer(stub, recordingMailer(true))

    const ran = await runDurableObjectAlarm(stub)
    expect(ran).toBe(true)

    const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
    expect(poll?.status).toBe('closed')

    expect(sent).toHaveLength(1)
    expect(sent[0]!.to).toBe(ownerEmail)

    expect(await getStorage(stub, 'deadline:at')).toBeUndefined()
    expect(await getAlarm(stub)).toBeNull()
  })

  it('deletes the alarm when the deadline is cleared', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const future = new Date(Date.now() + 60_000).toISOString()
    const { id: pollId } = await makePoll(db, ownerId, { deadlineAt: future })
    const stub = stubFor(pollId)

    await stub.syncDeadline(pollId, future)
    expect(await getAlarm(stub)).not.toBeNull()

    await stub.syncDeadline(pollId, null)
    expect(await getAlarm(stub)).toBeNull()
  })
})

describe('alarm — branch isolation', () => {
  it('does not reject when the deadline mailer throws, closes the poll, and still arms a pending digest afterwards', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const past = new Date(Date.now() - 60_000).toISOString()
    const { id: pollId } = await makePoll(db, ownerId, { deadlineAt: past })
    const stub = stubFor(pollId)

    const future = new Date(Date.now() + 60_000).toISOString()
    await stub.syncDeadline(pollId, future)

    // A pending digest, left due in the future — the deadline mailer throwing must not prevent
    // this from getting armed by the alarm's `finally`.
    await stub.enqueueDigest(pollId, { kind: 'vote', name: 'Ada', at: new Date().toISOString() })
    const digestAt = await getStorage<number>(stub, 'digest:at')

    // Force due *after* both RPCs above have finished re-arming (each re-arm reads current
    // storage) — otherwise the forced-past `deadline:at` would get picked up by `enqueueDigest`'s
    // own re-arm and scheduled for real, letting miniflare auto-fire it early with the *real*
    // mailer before `installMailer` below ever runs.
    await forceDue(stub, 'deadline:at')
    await installMailer(stub, throwingMailer)

    await expect(runDurableObjectAlarm(stub)).resolves.toBe(true)

    const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
    expect(poll?.status).toBe('closed')
    expect(await getStorage(stub, 'deadline:at')).toBeUndefined()

    expect(await getAlarm(stub)).toBe(digestAt)
  })

  it('still runs the deadline branch when the digest mailer throws, and applies digest retry bookkeeping', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const past = new Date(Date.now() - 60_000).toISOString()
    const { id: pollId } = await makePoll(db, ownerId, { deadlineAt: past })
    const stub = stubFor(pollId)

    const future = new Date(Date.now() + 60_000).toISOString()
    await stub.syncDeadline(pollId, future)
    await stub.enqueueDigest(pollId, { kind: 'vote', name: 'Ada', at: new Date().toISOString() })

    // Force both due only after both RPCs above have finished re-arming — see the comment in the
    // previous test for why ordering matters here.
    await forceDue(stub, 'deadline:at')
    await forceDue(stub, 'digest:at')
    await installMailer(stub, throwingMailer)

    await expect(runDurableObjectAlarm(stub)).resolves.toBe(true)

    const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
    expect(poll?.status).toBe('closed')
    expect(await getStorage(stub, 'deadline:at')).toBeUndefined()

    expect(await getStorage(stub, 'retry:count')).toBe(1)
    expect(await getStorage(stub, 'digest:items')).toHaveLength(1)
  })
})

describe('alarm — rearm ordering', () => {
  it('schedules the alarm for the earlier of digest and deadline', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const soon = new Date(Date.now() + 3 * 60_000).toISOString()
    const { id: pollId } = await makePoll(db, ownerId, { deadlineAt: soon })
    const stub = stubFor(pollId)

    await stub.enqueueDigest(pollId, { kind: 'vote', name: 'Ada', at: new Date().toISOString() })
    await stub.syncDeadline(pollId, soon)

    const alarmAt = await getAlarm(stub)
    const deadlineAtStored = await getStorage<number>(stub, 'deadline:at')
    expect(alarmAt).toBe(deadlineAtStored)
  })
})

describe('websocket fan-out', () => {
  it('rejects non-websocket requests with 426', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const stub = stubFor(pollId)

    const res = await stub.fetch(`https://do/ws?pollId=${pollId}`)
    expect(res.status).toBe(426)
  })

  it('accepts a websocket upgrade, sends presence, and fans out broadcasts', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const stub = stubFor(pollId)

    const res = await stub.fetch(`https://do/ws?pollId=${pollId}`, {
      headers: { Upgrade: 'websocket' },
    })
    expect(res.status).toBe(101)
    expect(res.webSocket).toBeTruthy()

    const client = res.webSocket!
    const messages: PollEvent[] = []
    const waiters: Array<() => void> = []
    client.addEventListener('message', (event: MessageEvent) => {
      messages.push(JSON.parse(event.data as string) as PollEvent)
      waiters.shift()?.()
    })
    client.accept()

    function waitForMessage(index: number): Promise<void> {
      if (messages.length > index) return Promise.resolve()
      return new Promise((resolve) => waiters.push(resolve))
    }

    await waitForMessage(0)
    expect(messages[0]).toEqual({ type: 'presence', count: 1 })

    const event: PollEvent = { type: 'poll.changed', entity: 'vote' }
    const count = await stub.broadcast(pollId, event)
    expect(count).toBe(1)

    await waitForMessage(1)
    expect(messages[1]).toEqual(event)
  })

  it('excludes the closing socket from the presence count broadcast on close', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const stub = stubFor(pollId)

    async function connect() {
      const res = await stub.fetch(`https://do/ws?pollId=${pollId}`, {
        headers: { Upgrade: 'websocket' },
      })
      const client = res.webSocket!
      const messages: PollEvent[] = []
      const waiters: Array<() => void> = []
      client.addEventListener('message', (event: MessageEvent) => {
        messages.push(JSON.parse(event.data as string) as PollEvent)
        waiters.shift()?.()
      })
      client.accept()
      function waitForMessage(index: number): Promise<void> {
        if (messages.length > index) return Promise.resolve()
        return new Promise((resolve) => waiters.push(resolve))
      }
      return { client, messages, waitForMessage }
    }

    const a = await connect()
    await a.waitForMessage(0)
    expect(a.messages[0]).toEqual({ type: 'presence', count: 1 })

    const b = await connect()
    await a.waitForMessage(1)
    expect(a.messages[1]).toEqual({ type: 'presence', count: 2 })

    // Closing b must broadcast a presence count that already excludes b, not the stale count
    // that still includes the closing socket.
    b.client.close()
    await a.waitForMessage(2)
    expect(a.messages[2]).toEqual({ type: 'presence', count: 1 })
  })
})
