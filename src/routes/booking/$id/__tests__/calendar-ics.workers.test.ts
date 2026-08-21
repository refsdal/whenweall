import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { getAuth } from '#/server/auth/auth'
import { localToUtcIso } from '#/lib/time'
import { createBooking } from '#/server/bookings/bookings'
import { makeBookingPage, makeUser } from '../../../../../test/helpers'
import { bookingIcsResponse } from '../calendar[.]ics'

const captchaHeaders = { 'x-captcha-response': 'test-token' }

async function signedInCookie(email: string, password: string): Promise<string> {
  const res = await getAuth().api.signInEmail({
    body: { email, password },
    headers: new Headers(captchaHeaders),
    asResponse: true,
  })
  const setCookie = res.headers.get('set-cookie')!
  return setCookie.split(';')[0]!
}

async function makeVerifiedOwner(): Promise<{ id: string; email: string; cookie: string }> {
  const email = `owner-${crypto.randomUUID()}@example.com`
  const password = 'correct horse battery staple'
  const signUp = await getAuth().api.signUpEmail({
    body: { name: 'Owner', email, password, locale: 'en' },
    headers: new Headers(captchaHeaders),
  })
  await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()
  const cookie = await signedInCookie(email, password)
  return { id: signUp.user.id, email, cookie }
}

const TUE_9AM = localToUtcIso('2026-08-25', '09:00', 'Europe/Oslo')

describe('GET /booking/$id/calendar.ics', () => {
  it('returns 401 when there is neither a manage token nor a session', async () => {
    const res = await bookingIcsResponse(new Request('https://x'), 'missing12345')
    expect(res.status).toBe(401)
  })

  it('returns 404 for a well-formed id that does not exist, token path', async () => {
    const res = await bookingIcsResponse(
      new Request('https://x?t=bogus-token-bogus-token-bogus-token-bogus'),
      'missing12345',
    )
    expect(res.status).toBe(404)
  })

  it('returns 403 for a wrong manage token', async () => {
    const db = createDb(env.DB)
    const owner = await makeVerifiedOwner()
    const { id: pageId } = await makeBookingPage(db, owner.id)
    const { bookingId } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      new Date('2026-08-20T00:00:00Z'),
    )

    const res = await bookingIcsResponse(new Request(`https://x?t=${'x'.repeat(43)}`), bookingId)
    expect(res.status).toBe(403)
  })

  it('returns 403 when the session user does not own the booking page', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { bookingId } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      new Date('2026-08-20T00:00:00Z'),
    )
    const other = await makeVerifiedOwner()

    const res = await bookingIcsResponse(
      new Request('https://x', { headers: { cookie: other.cookie } }),
      bookingId,
    )
    expect(res.status).toBe(403)
  })

  it('returns 200 with the ics calendar for a valid manage token', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)
    const { bookingId, manageToken } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      new Date('2026-08-20T00:00:00Z'),
    )

    const res = await bookingIcsResponse(new Request(`https://x?t=${manageToken}`), bookingId)
    expect(res.status).toBe(200)
    expect(res.headers.get('Content-Type')).toContain('text/calendar')
    expect(res.headers.get('Content-Disposition')).toBe(
      `attachment; filename="samla-booking-${bookingId}.ics"`,
    )
    expect(await res.text()).toContain('BEGIN:VCALENDAR')
  })

  it('returns 200 with the ics calendar for the owning organiser', async () => {
    const db = createDb(env.DB)
    const owner = await makeVerifiedOwner()
    const { id: pageId } = await makeBookingPage(db, owner.id)
    const { bookingId } = await createBooking(
      db,
      pageId,
      { startAt: TUE_9AM, name: 'Bob', email: 'bob@example.com', timezone: 'Europe/Oslo' },
      [],
      new Date('2026-08-20T00:00:00Z'),
    )

    const res = await bookingIcsResponse(
      new Request('https://x', { headers: { cookie: owner.cookie } }),
      bookingId,
    )
    expect(res.status).toBe(200)
    expect(await res.text()).toContain('BEGIN:VCALENDAR')
  })
})
