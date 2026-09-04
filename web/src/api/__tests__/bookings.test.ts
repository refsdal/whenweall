import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { ApiError } from '#/api/client'
import { getPublicAvailability, getPublicPage } from '#/api/bookings'

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
