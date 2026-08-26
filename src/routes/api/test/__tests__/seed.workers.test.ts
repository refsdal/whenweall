import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { member, user } from '#/server/db/schema'
import { getPublicPage } from '#/server/bookings/pages'
import { getEntitlements } from '#/server/billing/entitlements'
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

describe('POST /api/test/seed — plan', () => {
  it('inserts an active premium subscription for the seeded org when plan is "premium"', async () => {
    const res = await seedResponse({ plan: 'premium' })
    const body = (await res.json()) as { email: string }

    const db = createDb(env.DB)
    const seededUser = await db.query.user.findFirst({ where: eq(user.email, body.email) })
    const membership = await db.query.member.findFirst({
      where: eq(member.userId, seededUser!.id),
    })

    const entitlements = await getEntitlements(db, membership!.organizationId)
    expect(entitlements).toEqual({
      plan: 'premium',
      maxSeats: 10,
      googleSync: true,
      branding: true,
      push: true,
    })
  })

  it('leaves the org on the free plan when plan is omitted', async () => {
    const res = await seedResponse({})
    const body = (await res.json()) as { email: string }

    const db = createDb(env.DB)
    const seededUser = await db.query.user.findFirst({ where: eq(user.email, body.email) })
    const membership = await db.query.member.findFirst({
      where: eq(member.userId, seededUser!.id),
    })

    const entitlements = await getEntitlements(db, membership!.organizationId)
    expect(entitlements.plan).toBe('free')
  })
})
