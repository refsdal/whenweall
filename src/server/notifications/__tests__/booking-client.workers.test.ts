import { env } from 'cloudflare:workers'
import { runInDurableObject } from 'cloudflare:test'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { newId } from '#/lib/ids'
import { errorCode } from '#/lib/errors'
import { localToUtcIso } from '#/lib/time'
import {
  bookViaRoom,
  cancelViaRoom,
  notifyPageChanged,
  rescheduleViaRoom,
} from '#/server/notifications/booking-client'
import { makeBookingPage, makeUser } from '../../../../test/helpers'

function stubFor(pageId: string) {
  return env.BOOKING_ROOM.getByName(pageId)
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

// `hour` defaults to '10:00'; pass a different one when a test needs two slots guaranteed not to
// collide — rolling a weekend-landing `daysAhead` forward to the next Monday means two different
// `daysAhead` values can otherwise resolve to the same date (e.g. +2 and +3 from a Thursday both
// land on Monday).
function nextWeekdaySlot(daysAhead = 2, hour = '10:00'): string {
  let dateStr = localDateStr(new Date(Date.now() + daysAhead * 86_400_000), 'Europe/Oslo')
  while (!isWeekday(dateStr)) dateStr = addDaysToDateStr(dateStr, 1)
  return localToUtcIso(dateStr, hour, 'Europe/Oslo')
}

describe('notifyPageChanged', () => {
  it('resolves without throwing (best-effort) even for a page with no bookings yet', async () => {
    await expect(notifyPageChanged(newId())).resolves.toBeUndefined()
  })

  it('broadcasts page.changed to a connected client', async () => {
    const pageId = newId()
    const res = await stubFor(pageId).fetch('https://do/ws', {
      headers: { Upgrade: 'websocket' },
    })
    const client = res.webSocket!
    const messages: unknown[] = []
    const waiters: Array<() => void> = []
    client.addEventListener('message', (event: MessageEvent) => {
      messages.push(JSON.parse(event.data as string))
      waiters.shift()?.()
    })
    client.accept()
    function waitForMessage(index: number): Promise<void> {
      if (messages.length > index) return Promise.resolve()
      return new Promise((resolve) => waiters.push(resolve))
    }

    await notifyPageChanged(pageId)

    await waitForMessage(0)
    expect(messages[0]).toEqual({ type: 'page.changed' })
  })
})

describe('bookViaRoom / cancelViaRoom / rescheduleViaRoom', () => {
  it('bookViaRoom books through the room and returns a manage token', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const startAt = nextWeekdaySlot()

    const result = await bookViaRoom(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    expect(result.bookingId).toBeTruthy()
    expect(result.manageToken).toMatch(/^[A-Za-z0-9_-]{43}$/)
  })

  it('propagates a business error instead of swallowing it (NOT best-effort)', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const startAt = nextWeekdaySlot()

    await bookViaRoom(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )

    let caught: unknown
    try {
      await bookViaRoom(
        pageId,
        { startAt, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
        [],
      )
    } catch (err) {
      caught = err
    }
    expect(errorCode(caught)).toBe('SLOT_UNAVAILABLE')
  })

  it('cancelViaRoom cancels through the room', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const startAt = nextWeekdaySlot()

    const { bookingId } = await bookViaRoom(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    const result = await cancelViaRoom(pageId, bookingId, 'organiser')
    expect(result.changed).toBe(true)
  })

  it('rescheduleViaRoom moves the booking through the room', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const startAt = nextWeekdaySlot(2, '10:00')
    const newStartAt = nextWeekdaySlot(3, '13:00')

    const { bookingId } = await bookViaRoom(
      pageId,
      { startAt, name: 'Alice', email: 'alice@example.com', timezone: 'Europe/Oslo' },
      [],
    )
    const result = await rescheduleViaRoom(pageId, bookingId, newStartAt, [])
    expect(result.changed).toBe(true)
    expect(result.previousStartAt).toBe(startAt)
    // `runInDurableObject` re-enters the same DO instance the room helpers above already used —
    // proves the reminder key really did move.
    const stored = await runInDurableObject(stubFor(pageId), (_instance, state) =>
      state.storage.get<number>(`reminder:${bookingId}`),
    )
    expect(stored).toBe(new Date(newStartAt).getTime())
  })
})
