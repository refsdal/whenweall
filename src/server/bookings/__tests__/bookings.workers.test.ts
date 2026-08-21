import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { AppError } from '#/lib/errors'
import { localToUtcIso } from '#/lib/time'
import { updatePage } from '#/server/bookings/pages'
import {
  bookedIntervals,
  cancelBooking,
  createBooking,
  getBookingForManage,
  listBookings,
  rescheduleBooking,
} from '#/server/bookings/bookings'
import { makeBooking, makeBookingPage, makeUser } from '../../../../test/helpers'

const NOW = new Date('2026-08-20T00:00:00Z')
// 2026-08-25 is a Tuesday; the default makeBookingPage fixture is available Mon–Fri 09:00–17:00.
const TUE_9AM = localToUtcIso('2026-08-25', '09:00', 'Europe/Oslo')
const TUE_930AM = localToUtcIso('2026-08-25', '09:30', 'Europe/Oslo')

describe('createBooking', () => {
  it('creates a confirmed booking and returns a 43-char manage token', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)

    const { bookingId, manageToken } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      NOW,
    )

    expect(bookingId).toBeTruthy()
    expect(manageToken).toMatch(/^[A-Za-z0-9_-]{43}$/)

    const view = await getBookingForManage(db, bookingId, { token: manageToken })
    expect(view.startAt).toBe(TUE_9AM)
    expect(view.status).toBe('confirmed')
  })

  it('throws PAGE_PAUSED for a paused page', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    await updatePage(db, pageId, ownerId, { status: 'paused' })

    await expect(
      createBooking(
        db,
        pageId,
        { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
        [],
        NOW,
      ),
    ).rejects.toMatchObject(new AppError('PAGE_PAUSED'))
  })

  it('throws BOOKING_PAST when startAt is before now', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)

    await expect(
      createBooking(
        db,
        pageId,
        { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
        [],
        new Date('2026-08-26T00:00:00Z'),
      ),
    ).rejects.toMatchObject(new AppError('BOOKING_PAST'))
  })

  it('throws SLOT_UNAVAILABLE outside availability', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const outsideHours = localToUtcIso('2026-08-25', '08:00', 'Europe/Oslo')

    await expect(
      createBooking(
        db,
        pageId,
        { startAt: outsideHours, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
        [],
        NOW,
      ),
    ).rejects.toMatchObject(new AppError('SLOT_UNAVAILABLE'))
  })

  it('throws SLOT_UNAVAILABLE when the candidate collides with an existing booking plus buffer', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, {
      bufferBeforeMin: 15,
      bufferAfterMin: 15,
    })
    await makeBooking(db, pageId, TUE_9AM, { endAt: TUE_930AM })

    // Adjacent slot: 09:30–10:00 — its own 15-min bufferBeforeMin pads it back to 09:15, which
    // collides with the (unbuffered, stored-raw) 09:00–09:30 booking.
    await expect(
      createBooking(
        db,
        pageId,
        { startAt: TUE_930AM, name: 'Carol', email: 'carol@example.com', timezone: 'Europe/Oslo' },
        [],
        NOW,
      ),
    ).rejects.toMatchObject(new AppError('SLOT_UNAVAILABLE'))
  })

  it('buffers apply once (via the candidate), not twice via the stored busy interval', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    // 15-min slots so 09:30/09:45/10:30/10:45 (quarter-hour offsets from the 10:00 booking) are
    // all valid slot starts on the generateSlots grid — the existing 10:00–10:30 booking itself
    // is seeded as a raw row via makeBooking, so it doesn't need to be grid-aligned.
    const { id: pageId } = await makeBookingPage(db, ownerId, {
      slotDurationMin: 15,
      bufferBeforeMin: 15,
      bufferAfterMin: 15,
    })
    const tenAm = localToUtcIso('2026-08-25', '10:00', 'Europe/Oslo')
    const tenThirtyAm = localToUtcIso('2026-08-25', '10:30', 'Europe/Oslo')
    await makeBooking(db, pageId, tenAm, { endAt: tenThirtyAm })

    async function attempt(time: string, email: string) {
      return createBooking(
        db,
        pageId,
        {
          startAt: localToUtcIso('2026-08-25', time, 'Europe/Oslo'),
          name: 'Visitor',
          email,
          timezone: 'Europe/Oslo',
        },
        [],
        NOW,
      )
    }

    // A 15-min gap after the booking's end is exactly the configured bufferAfterMin, so a slot
    // starting there succeeds — if buffers were double-applied (once on the stored interval via
    // bookedIntervals, once again on the candidate in generateSlots) this would need a 30-min gap
    // and wrongly fail.
    await expect(attempt('10:45', 'a@example.com')).resolves.toMatchObject({
      bookingId: expect.any(String),
    })
    // Touching the booking's end (no gap) still fails: the candidate's own 15-min
    // bufferBeforeMin pads it back into the existing booking.
    await expect(attempt('10:30', 'b@example.com')).rejects.toMatchObject(
      new AppError('SLOT_UNAVAILABLE'),
    )

    // Symmetric on the other side: a 15-min gap *before* the booking's start succeeds...
    await expect(attempt('09:30', 'c@example.com')).resolves.toMatchObject({
      bookingId: expect.any(String),
    })
    // ...but touching the booking's start (no gap) does not.
    await expect(attempt('09:45', 'd@example.com')).rejects.toMatchObject(
      new AppError('SLOT_UNAVAILABLE'),
    )
  })

  it('rejects a slot blocked by a caller-supplied busy interval (e.g. Google Calendar)', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)

    await expect(
      createBooking(
        db,
        pageId,
        { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
        [{ start: TUE_9AM, end: TUE_930AM }],
        NOW,
      ),
    ).rejects.toMatchObject(new AppError('SLOT_UNAVAILABLE'))
  })
})

describe('bookedIntervals', () => {
  it('returns confirmed bookings as raw [start, end) intervals (no buffer applied) and excludes cancelled ones', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    // Buffers configured but must NOT show up in the returned intervals — generateSlots is the
    // single place buffers apply (applied to whichever candidate slot it's checking), so
    // expanding the stored interval here too would double-apply them.
    const { id: pageId } = await makeBookingPage(db, ownerId, {
      bufferBeforeMin: 5,
      bufferAfterMin: 10,
    })
    await makeBooking(db, pageId, TUE_9AM, { endAt: TUE_930AM })
    await makeBooking(db, pageId, TUE_930AM, { status: 'cancelled', cancelledBy: 'visitor' })

    const intervals = await bookedIntervals(db, pageId, {
      from: new Date('2026-08-25T00:00:00Z'),
      to: new Date('2026-08-26T00:00:00Z'),
    })

    expect(intervals).toEqual([{ start: TUE_9AM, end: TUE_930AM }])
  })
})

describe('cancelBooking', () => {
  it('cancels a confirmed booking and is idempotent on a second call', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { id: bookingId } = await makeBooking(db, pageId, TUE_9AM)

    await expect(cancelBooking(db, bookingId, 'visitor')).resolves.toEqual({ changed: true })
    await expect(cancelBooking(db, bookingId, 'organiser')).resolves.toEqual({ changed: false })
  })

  it('throws NOT_FOUND for an unknown booking', async () => {
    const db = createDb(env.DB)
    await expect(cancelBooking(db, 'missing', 'visitor')).rejects.toMatchObject(
      new AppError('NOT_FOUND'),
    )
  })
})

describe('rescheduleBooking', () => {
  it('moves the booking, keeps the manage token, and does not block on its own prior slot', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { bookingId, manageToken } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      NOW,
    )

    const newStart = localToUtcIso('2026-08-25', '10:00', 'Europe/Oslo')
    const result = await rescheduleBooking(db, bookingId, newStart, [], NOW)
    expect(result).toEqual({ changed: true, previousStartAt: TUE_9AM })

    const view = await getBookingForManage(db, bookingId, { token: manageToken })
    expect(view.startAt).toBe(newStart)
  })

  it('rescheduling to the exact same slot succeeds (its own interval does not self-block)', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, {
      bufferBeforeMin: 15,
      bufferAfterMin: 15,
    })
    const { bookingId } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      NOW,
    )

    await expect(rescheduleBooking(db, bookingId, TUE_9AM, [], NOW)).resolves.toEqual({
      changed: true,
      previousStartAt: TUE_9AM,
    })
  })

  it('throws SLOT_UNAVAILABLE when the new slot collides with another booking', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { bookingId } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      NOW,
    )
    await makeBooking(db, pageId, TUE_930AM)

    await expect(rescheduleBooking(db, bookingId, TUE_930AM, [], NOW)).rejects.toMatchObject(
      new AppError('SLOT_UNAVAILABLE'),
    )
  })
})

describe('getBookingForManage', () => {
  it('authorises by token, by owner, and rejects both a bad token and a non-owner', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { bookingId, manageToken } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      NOW,
    )

    await expect(getBookingForManage(db, bookingId, { token: manageToken })).resolves.toMatchObject(
      { id: bookingId },
    )
    await expect(getBookingForManage(db, bookingId, { ownerId })).resolves.toMatchObject({
      id: bookingId,
    })
    await expect(
      getBookingForManage(db, bookingId, { token: 'wrong-token' }),
    ).rejects.toMatchObject(new AppError('INVALID_TOKEN'))
    await expect(getBookingForManage(db, bookingId, { ownerId: otherId })).rejects.toMatchObject(
      new AppError('FORBIDDEN'),
    )
    await expect(getBookingForManage(db, 'missing', { ownerId })).rejects.toMatchObject(
      new AppError('NOT_FOUND'),
    )
  })
})

describe('listBookings', () => {
  it('returns bookings within range for the owner and rejects a non-owner', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    await makeBooking(db, pageId, TUE_9AM)
    const outOfRange = localToUtcIso('2026-09-25', '09:00', 'Europe/Oslo')
    await makeBooking(db, pageId, outOfRange)

    const rows = await listBookings(db, pageId, ownerId, {
      from: new Date('2026-08-01T00:00:00Z'),
      to: new Date('2026-09-01T00:00:00Z'),
    })
    expect(rows).toHaveLength(1)
    expect(rows[0]?.startAt).toBe(TUE_9AM)

    await expect(listBookings(db, pageId, otherId, { from: NOW, to: NOW })).rejects.toMatchObject(
      new AppError('FORBIDDEN'),
    )
    await expect(
      listBookings(db, 'missing', ownerId, { from: NOW, to: NOW }),
    ).rejects.toMatchObject(new AppError('NOT_FOUND'))
  })
})
