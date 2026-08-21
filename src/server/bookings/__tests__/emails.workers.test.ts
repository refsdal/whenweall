import { env } from 'cloudflare:workers'
import { describe, expect, it, vi } from 'vitest'
import { createDb } from '#/server/db/client'
import { setUserHandle } from '#/server/bookings/pages'
import { sendBookingEmails, sendGoogleSyncFailedNotice } from '#/server/bookings/emails'
import { createBooking } from '#/server/bookings/bookings'
import { makeBooking, makeBookingPage, makeUser } from '../../../../test/helpers'
import { localToUtcIso } from '#/lib/time'

const TUE_9AM = localToUtcIso('2026-08-25', '09:00', 'Europe/Oslo')

const testEnv = {
  EMAIL_FROM: 'samla <no-reply@samla.test>',
  APP_URL: 'https://samla.test',
  APP_ENV: 'test',
}

describe('sendBookingEmails', () => {
  it('sends confirmed emails to visitor + owner with an ics attachment', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, { name: 'Ada', email: 'ada-confirmed@example.com' })
    await setUserHandle(db, ownerId, 'ada-confirmed')
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { bookingId, manageToken } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      new Date('2026-08-20T00:00:00Z'),
    )

    const mailer = vi.fn().mockResolvedValue(true)
    const result = await sendBookingEmails(testEnv, 'confirmed', bookingId, {
      db,
      mailer,
      manageToken,
    })

    expect(result).toEqual({ sent: 2, failed: 0 })
    expect(mailer).toHaveBeenCalledTimes(2)

    const [visitorCall, ownerCall] = mailer.mock.calls
    expect(visitorCall?.[1].to).toBe('bob@example.com')
    expect(visitorCall?.[1].html).toContain(manageToken)
    expect(visitorCall?.[1].attachments?.[0]?.filename).toBe('calendar.ics')
    expect(ownerCall?.[1].to).toBe('ada-confirmed@example.com')
    expect(ownerCall?.[1].html).toContain('Bob')
  })

  it('sends cancelled emails without an ics attachment', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, { name: 'Ada', email: 'ada-cancelled@example.com' })
    await setUserHandle(db, ownerId, 'ada-cancelled')
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { id: bookingId } = await makeBooking(db, pageId, TUE_9AM, {
      status: 'cancelled',
      cancelledBy: 'visitor',
      visitorEmail: 'bob@example.com',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    const result = await sendBookingEmails(testEnv, 'cancelled', bookingId, { db, mailer })

    expect(result).toEqual({ sent: 2, failed: 0 })
    for (const call of mailer.mock.calls) {
      expect(call[1].attachments).toBeUndefined()
    }
  })

  it('is best-effort: returns zero counts (never throws) for an unknown booking', async () => {
    const db = createDb(env.DB)
    const mailer = vi.fn().mockResolvedValue(true)

    await expect(
      sendBookingEmails(testEnv, 'reminder', 'missing-booking', { db, mailer }),
    ).resolves.toEqual({ sent: 0, failed: 0 })
    expect(mailer).not.toHaveBeenCalled()
  })

  it('sends rescheduled emails to visitor + owner, mentioning the previous and new time', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, {
      name: 'Ada',
      email: 'ada-rescheduled@example.com',
    })
    await setUserHandle(db, ownerId, 'ada-rescheduled')
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const WED_10AM = localToUtcIso('2026-08-26', '10:00', 'Europe/Oslo')
    const { id: bookingId } = await makeBooking(db, pageId, WED_10AM, {
      visitorEmail: 'bob-rescheduled@example.com',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    const result = await sendBookingEmails(testEnv, 'rescheduled', bookingId, {
      db,
      mailer,
      previousStartAt: TUE_9AM,
    })

    expect(result).toEqual({ sent: 2, failed: 0 })
    const [visitorCall, ownerCall] = mailer.mock.calls
    expect(visitorCall?.[1].to).toBe('bob-rescheduled@example.com')
    // Both the previous slot's date (25) and the new one's (26) show up in the visitor email.
    expect(visitorCall?.[1].html).toContain('25')
    expect(visitorCall?.[1].html).toContain('26')
    expect(visitorCall?.[1].attachments?.[0]?.filename).toBe('calendar.ics')
    expect(ownerCall?.[1].to).toBe('ada-rescheduled@example.com')
  })

  it('sends the organiser a dedicated "booking moved" notice, not "New booking"', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, {
      name: 'Ada',
      email: 'ada-org-rescheduled@example.com',
    })
    await setUserHandle(db, ownerId, 'ada-org-rescheduled')
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const WED_10AM = localToUtcIso('2026-08-26', '10:00', 'Europe/Oslo')
    const { id: bookingId } = await makeBooking(db, pageId, WED_10AM, {
      visitorEmail: 'bob-org-rescheduled@example.com',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    const result = await sendBookingEmails(testEnv, 'rescheduled', bookingId, {
      db,
      mailer,
      previousStartAt: TUE_9AM,
    })

    expect(result).toEqual({ sent: 2, failed: 0 })
    const [, ownerCall] = mailer.mock.calls
    expect(ownerCall?.[1].to).toBe('ada-org-rescheduled@example.com')
    expect(ownerCall?.[1].subject).not.toContain('New booking')
    // Both the previous slot's date (25) and the new one's (26) show up for the organiser too.
    expect(ownerCall?.[1].html).toContain('25')
    expect(ownerCall?.[1].html).toContain('26')
  })

  it('counts a mailer failure without throwing', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, { name: 'Ada', email: 'ada-failure@example.com' })
    await setUserHandle(db, ownerId, 'ada-failure')
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { id: bookingId } = await makeBooking(db, pageId, TUE_9AM, {
      visitorEmail: 'bob@example.com',
    })

    const mailer = vi.fn().mockResolvedValue(false)
    const result = await sendBookingEmails(testEnv, 'reminder', bookingId, { db, mailer })

    expect(result).toEqual({ sent: 0, failed: 2 })
  })
})

describe('sendGoogleSyncFailedNotice', () => {
  it('sends a best-effort notice to the organiser, mentioning the page title', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, { name: 'Ada', email: 'ada-sync@example.com' })
    await setUserHandle(db, ownerId, 'ada-sync')
    const { id: pageId } = await makeBookingPage(db, ownerId, { title: 'Coffee chat' })
    const { id: bookingId } = await makeBooking(db, pageId, TUE_9AM, {
      visitorEmail: 'bob@example.com',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    await sendGoogleSyncFailedNotice(testEnv, bookingId, { db, mailer })

    expect(mailer).toHaveBeenCalledTimes(1)
    const [, message] = mailer.mock.calls[0]!
    expect(message.to).toBe('ada-sync@example.com')
    expect(message.subject).toContain('Coffee chat')
  })

  it('is best-effort: does nothing (never throws) for an unknown booking', async () => {
    const db = createDb(env.DB)
    const mailer = vi.fn().mockResolvedValue(true)

    await expect(
      sendGoogleSyncFailedNotice(testEnv, 'missing-booking', { db, mailer }),
    ).resolves.toBeUndefined()
    expect(mailer).not.toHaveBeenCalled()
  })

  it('swallows a mailer failure without throwing', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, {
      name: 'Ada',
      email: 'ada-sync-fail@example.com',
    })
    await setUserHandle(db, ownerId, 'ada-sync-fail')
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { id: bookingId } = await makeBooking(db, pageId, TUE_9AM, {
      visitorEmail: 'bob@example.com',
    })

    const mailer = vi.fn().mockRejectedValue(new Error('smtp down'))
    await expect(
      sendGoogleSyncFailedNotice(testEnv, bookingId, { db, mailer }),
    ).resolves.toBeUndefined()
  })
})
