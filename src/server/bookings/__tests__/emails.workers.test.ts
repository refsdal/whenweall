import { env } from 'cloudflare:workers'
import { describe, expect, it, vi } from 'vitest'
import { createDb } from '#/server/db/client'
import { setUserHandle } from '#/server/bookings/pages'
import { sendBookingEmails } from '#/server/bookings/emails'
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
