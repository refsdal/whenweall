import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { ApiError } from '#/api/client'
import { bookSlot, getPublicAvailability, getPublicPage, updateBookingPageSchema } from '#/api/bookings'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const notFound = () =>
  HttpResponse.json({ error: { code: 'not_found', message: 'not found' } }, { status: 404 })

describe('getPublicPage', () => {
  it('resolves null for an unknown handle/slug (404 not_found), so the route can throw notFound()', async () => {
    server.use(http.get('/api/v1/book/ada/missing', notFound))

    expect(await getPublicPage('ada', 'missing')).toBeNull()
  })

  it('still throws every other ApiError', async () => {
    server.use(
      http.get('/api/v1/book/ada/intro', () =>
        HttpResponse.json({ error: { code: 'rate_limited', message: 'slow down' } }, { status: 429 }),
      ),
    )

    await expect(getPublicPage('ada', 'intro')).rejects.toBeInstanceOf(ApiError)
  })
})

describe('getPublicAvailability', () => {
  it('resolves null for an unknown page even though the availability call 404s too', async () => {
    server.use(
      http.get('/api/v1/book/ada/missing', notFound),
      http.get('/api/v1/book/ada/missing/availability', notFound),
    )

    expect(
      await getPublicAvailability({ handle: 'ada', slug: 'missing', from: '2026-09-01', to: '2026-10-02' }),
    ).toBeNull()
  })

  it('pairs each slot start with an end computed from slotDurationMin', async () => {
    server.use(
      http.get('/api/v1/book/ada/intro', () =>
        HttpResponse.json({
          id: 'pg_1', handle: 'ada', slug: 'intro', title: 'Intro', description: null, location: null,
          timezone: 'UTC', slotDurationMin: 30, maxDaysAhead: 60, status: 'active', owner: { name: 'Ada' },
        }),
      ),
      http.get('/api/v1/book/ada/intro/availability', () =>
        HttpResponse.json({ slots: ['2026-09-15T07:00:00.000Z'] }),
      ),
    )

    const result = await getPublicAvailability({ handle: 'ada', slug: 'intro', from: '2026-09-01', to: '2026-10-02' })
    expect(result?.slots).toEqual([
      { start: '2026-09-15T07:00:00.000Z', end: '2026-09-15T07:30:00.000Z' },
    ])
  })
})

const validCreateInput = {
  slug: 'intro-call',
  title: 'Intro call',
  timezone: 'Europe/Oslo',
  slotDurationMin: 30,
  bufferBeforeMin: 0,
  bufferAfterMin: 0,
  minNoticeMin: 0,
  maxDaysAhead: 60,
  availability: { '1': [{ start: '09:00', end: '17:00' }] },
  googleSync: false,
  reminders: true,
}

describe('updateBookingPageSchema', () => {
  it('is a full replacement: every create field plus status is required', () => {
    expect(updateBookingPageSchema.safeParse({ pageId: 'p1', status: 'active' }).success).toBe(false)
    expect(updateBookingPageSchema.safeParse({ ...validCreateInput, pageId: 'p1' }).success).toBe(false)
    expect(
      updateBookingPageSchema.safeParse({ ...validCreateInput, pageId: 'p1', status: 'paused' }).success,
    ).toBe(true)
  })
})

describe('bookSlot', () => {
  it('sends the visitor locale so the confirmation mail renders in their language', async () => {
    let seenBody: Record<string, unknown> | null = null
    server.use(
      http.post('/api/v1/book/ada/intro/bookings', async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(
          { booking: { id: 'bk_1' }, manageToken: 'tok' },
          { status: 201 },
        )
      }),
    )

    await bookSlot('ada', 'intro', {
      startAt: '2026-09-15T07:00:00.000Z',
      name: 'Ada',
      email: 'ada@example.com',
      timezone: 'Europe/Oslo',
    })

    // paraglide's runtime resolves the base locale ("en") under vitest.
    expect(seenBody).toMatchObject({ locale: 'en', timezone: 'Europe/Oslo' })
  })
})
