import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { getPublicPage } from '#/server/bookings/pages'
import { seedResponse } from '../seed'

describe('POST /api/test/seed — withBookingPage', () => {
  it('creates a weekday 09-17 Oslo, 30-min booking page and sets a unique handle', async () => {
    const res = await seedResponse({ withBookingPage: true })
    expect(res.status).toBe(200)

    const body = (await res.json()) as {
      pageId: string | null
      handle: string | null
      slug: string | null
    }
    expect(body.pageId).toBeTruthy()
    expect(body.handle).toMatch(/^test-/)
    expect(body.slug).toBe('intro-call')

    const db = createDb(env.DB)
    const page = await getPublicPage(db, body.handle!, body.slug!)
    expect(page).not.toBeNull()
    expect(page!.id).toBe(body.pageId)
    expect(page!.slotDurationMin).toBe(30)
    expect(page!.timezone).toBe('Europe/Oslo')
    expect(page!.availability['1']).toEqual([{ start: '09:00', end: '17:00' }])
    expect(page!.status).toBe('active')
  })

  it('does not create a booking page or set a handle when withBookingPage is omitted', async () => {
    const res = await seedResponse({})
    const body = (await res.json()) as { pageId: string | null; handle: string | null }
    expect(body.pageId).toBeNull()
    expect(body.handle).toBeNull()
  })

  it('two seeded users each get their own unique handle', async () => {
    const a = (await (await seedResponse({ withBookingPage: true })).json()) as { handle: string }
    const b = (await (await seedResponse({ withBookingPage: true })).json()) as { handle: string }
    expect(a.handle).not.toBe(b.handle)
  })
})
