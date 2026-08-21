import { env } from 'cloudflare:workers'
import { runDurableObjectAlarm, runInDurableObject } from 'cloudflare:test'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { errorCode } from '#/lib/errors'
import { localToUtcIso } from '#/lib/time'
import type { MailMessage } from '#/server/mailer/mailer'
import { BookingRoom } from '#/do/BookingRoom'
import type { BookingRoomEvent } from '#/do/booking-protocol'
import { cancelBooking } from '#/server/bookings/bookings'
import { makeBookingPage, makeUser } from '../../../test/helpers'

// `vi.mock` cannot reach code running inside a Durable Object's own module graph in
// @cloudflare/vitest-plugin 1.0 — `alarm()`/RPC methods load via a separate `importModule()` path
// that does not share the test file's mock registry. `BookingRoom.mailer` is an overridable
// instance field (default: the real `sendMail`) that tests set directly through
// `runInDurableObject`.

const REMINDER_LEAD_MS = 24 * 60 * 60_000

function stubFor(pageId: string) {
  return env.BOOKING_ROOM.getByName(pageId)
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

async function putStorage(
  stub: ReturnType<typeof stubFor>,
  key: string,
  value: unknown,
): Promise<void> {
  await runInDurableObject(stub, (_instance, state) => state.storage.put(key, value))
}

async function installMailer(
  stub: ReturnType<typeof stubFor>,
  impl: (env: unknown, msg: MailMessage) => Promise<boolean>,
): Promise<void> {
  await runInDurableObject(stub, (instance: BookingRoom) => {
    instance.mailer = impl as BookingRoom['mailer']
  })
}

function localDateStr(d: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(d)
}

function addDaysToDateStr(dateStr: string, days: number): string {
  const [y, m, d] = dateStr.split('-').map(Number)
  const dt = new Date(Date.UTC(y, m - 1, d))
  dt.setUTCDate(dt.getUTCDate() + days)
  return dt.toISOString().slice(0, 10)
}

function isWeekday(dateStr: string): boolean {
  const day = new Date(`${dateStr}T00:00:00Z`).getUTCDay()
  return day >= 1 && day <= 5
}

/** A weekday Europe/Oslo slot at least `daysAhead` days out — inside the default
 * `makeBookingPage` fixture's Mon–Fri 09:00–17:00 availability, safely in the future regardless of
 * when the suite runs, and unaffected by min-notice (0) or the 60-day horizon. `hour` defaults to
 * '10:00'; pass a different one when a test needs two slots guaranteed not to collide — rolling a
 * weekend-landing `daysAhead` forward to the next Monday means two different `daysAhead` values
 * can otherwise resolve to the same date (e.g. +2 and +3 from a Thursday both land on Monday). */
function nextWeekdaySlot(daysAhead = 2, hour = '10:00'): string {
  let dateStr = localDateStr(new Date(Date.now() + daysAhead * 86_400_000), 'Europe/Oslo')
  while (!isWeekday(dateStr)) dateStr = addDaysToDateStr(dateStr, 1)
  return localToUtcIso(dateStr, hour, 'Europe/Oslo')
}

async function connect(stub: ReturnType<typeof stubFor>) {
  const res = await stub.fetch('https://do/ws', { headers: { Upgrade: 'websocket' } })
  const client = res.webSocket!
  const messages: BookingRoomEvent[] = []
  const waiters: Array<() => void> = []
  client.addEventListener('message', (event: MessageEvent) => {
    messages.push(JSON.parse(event.data as string) as BookingRoomEvent)
    waiters.shift()?.()
  })
  client.accept()
  function waitForMessage(index: number): Promise<void> {
    if (messages.length > index) return Promise.resolve()
    return new Promise((resolve) => waiters.push(resolve))
  }
  return { messages, waitForMessage }
}

describe('websocket upgrade', () => {
  it('rejects non-websocket requests with 426', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)

    const res = await stub.fetch('https://do/ws')
    expect(res.status).toBe(426)
  })
})

describe('book RPC', () => {
  it('creates a booking and broadcasts page.changed', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const { messages, waitForMessage } = await connect(stub)

    const startAt = nextWeekdaySlot()
    const result = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    expect(result.bookingId).toBeTruthy()
    expect(result.manageToken).toMatch(/^[A-Za-z0-9_-]{43}$/)

    await waitForMessage(0)
    expect(messages[0]).toEqual({ type: 'page.changed' })
  })

  it('serialises concurrent book calls on the same slot: exactly one succeeds', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot()

    const results = await Promise.allSettled([
      stub.book(
        pageId,
        { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
        [],
      ),
      stub.book(
        pageId,
        { startAt, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
        [],
      ),
    ])

    const fulfilled = results.filter((r) => r.status === 'fulfilled')
    const rejected = results.filter((r) => r.status === 'rejected') as PromiseRejectedResult[]
    expect(fulfilled).toHaveLength(1)
    expect(rejected).toHaveLength(1)
    expect(errorCode(rejected[0]!.reason)).toBe('SLOT_UNAVAILABLE')
  })

  it('schedules a reminder alarm 24h before the slot when the page has reminders on', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot()

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )

    const stored = await getStorage<number>(stub, `reminder:${bookingId}`)
    expect(stored).toBe(new Date(startAt).getTime())
    expect(await getAlarm(stub)).toBe(stored! - REMINDER_LEAD_MS)
  })

  it('does not schedule a reminder when the page has reminders off', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, { reminders: false })
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot()

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )

    expect(await getStorage(stub, `reminder:${bookingId}`)).toBeUndefined()
    expect(await getAlarm(stub)).toBeNull()
  })
})

describe('cancel RPC', () => {
  it('cancels a booking, broadcasts page.changed, and clears its reminder', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot()

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    expect(await getAlarm(stub)).not.toBeNull()

    const { messages, waitForMessage } = await connect(stub)
    const result = await stub.cancel(pageId, bookingId, 'organiser')
    expect(result.changed).toBe(true)

    await waitForMessage(0)
    expect(messages[0]).toEqual({ type: 'page.changed' })
    expect(await getStorage(stub, `reminder:${bookingId}`)).toBeUndefined()
    expect(await getAlarm(stub)).toBeNull()
  })

  it('is idempotent: cancelling an already-cancelled booking is a no-op, no broadcast', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot()

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    await stub.cancel(pageId, bookingId, 'organiser')

    const { messages, waitForMessage } = await connect(stub)
    const result = await stub.cancel(pageId, bookingId, 'organiser')
    expect(result.changed).toBe(false)

    // Prove nothing arrives: a broadcast after the no-op is the next message this socket sees.
    await stub.broadcast({ type: 'page.changed' })
    await waitForMessage(0)
    expect(messages[0]).toEqual({ type: 'page.changed' })
  })
})

describe('reschedule RPC', () => {
  it('moves the booking, broadcasts page.changed, and re-arms the reminder for the new time', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot(2, '10:00')
    const newStartAt = nextWeekdaySlot(3, '13:00')

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )

    const { messages, waitForMessage } = await connect(stub)
    const result = await stub.reschedule(pageId, bookingId, newStartAt, [])
    expect(result.changed).toBe(true)
    expect(result.previousStartAt).toBe(startAt)

    await waitForMessage(0)
    expect(messages[0]).toEqual({ type: 'page.changed' })

    const stored = await getStorage<number>(stub, `reminder:${bookingId}`)
    expect(stored).toBe(new Date(newStartAt).getTime())
  })

  it('propagates SLOT_UNAVAILABLE when the new slot is already taken', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot(2, '10:00')
    const takenStartAt = nextWeekdaySlot(3, '13:00')

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    await stub.book(
      pageId,
      { startAt: takenStartAt, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
    )

    let caught: unknown
    try {
      await stub.reschedule(pageId, bookingId, takenStartAt, [])
    } catch (err) {
      caught = err
    }
    expect(errorCode(caught)).toBe('SLOT_UNAVAILABLE')
  })
})

describe('reminder alarm', () => {
  it('sends reminder mail to both parties via the DI mailer and clears the key', async () => {
    const db = createDb(env.DB)
    const { id: ownerId, email: ownerEmail } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot()

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )

    // Force the reminder due "now" instead of waiting for real time to pass (see the equivalent
    // `forceDue` helper in `poll-room.workers.test.ts`): overwrite the tracking key directly
    // rather than scheduling the DO's real alarm in the past, which would race miniflare's own
    // scheduler.
    await putStorage(stub, `reminder:${bookingId}`, Date.now() - 1000)

    const sent: MailMessage[] = []
    await installMailer(stub, async (_env, msg) => {
      sent.push(msg)
      return true
    })

    const ran = await runDurableObjectAlarm(stub)
    expect(ran).toBe(true)

    expect(sent.map((m) => m.to)).toEqual(expect.arrayContaining(['alice@example.com', ownerEmail]))
    expect(await getStorage(stub, `reminder:${bookingId}`)).toBeUndefined()
    expect(await getAlarm(stub)).toBeNull()
  })

  it('skips a booking that is no longer confirmed but still clears its key', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)
    const startAt = nextWeekdaySlot()

    const { bookingId } = await stub.book(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    // Cancel via the service directly (bypassing the DO) so the reminder key is left stale, as if
    // the booking had been cancelled through some other path.
    await cancelBooking(db, bookingId, 'organiser')
    await putStorage(stub, `reminder:${bookingId}`, Date.now() - 1000)

    const sent: MailMessage[] = []
    await installMailer(stub, async (_env, msg) => {
      sent.push(msg)
      return true
    })

    await runDurableObjectAlarm(stub)

    expect(sent).toHaveLength(0)
    expect(await getStorage(stub, `reminder:${bookingId}`)).toBeUndefined()
  })

  it('re-arms to the next-earliest reminder after one fires', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const stub = stubFor(pageId)

    const soon = nextWeekdaySlot(2, '10:00')
    const later = nextWeekdaySlot(3, '13:00')
    const first = await stub.book(
      pageId,
      { startAt: soon, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    await stub.book(
      pageId,
      { startAt: later, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
    )

    await putStorage(stub, `reminder:${first.bookingId}`, Date.now() - 1000)
    await installMailer(stub, async () => true)

    await runDurableObjectAlarm(stub)

    const laterStored = await getStorage<number>(stub, `reminder:${first.bookingId}`)
    // The first booking's key is gone (it fired); the second is still armed for its own time.
    expect(laterStored).toBeUndefined()
    expect(await getAlarm(stub)).toBe(new Date(later).getTime() - REMINDER_LEAD_MS)
  })
})
