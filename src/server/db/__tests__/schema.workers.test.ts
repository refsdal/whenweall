import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import {
  polls,
  pollOptions,
  user,
  votes,
  participants,
  organization,
  bookingPages,
  bookings,
} from '#/server/db/schema'

describe('schema', () => {
  it('inserts a user, an org, a poll with options, a participant and votes, and cascades on delete', async () => {
    const db = createDb(env.DB)
    const now = new Date().toISOString()
    await db.insert(user).values({
      id: 'u1',
      name: 'Ada',
      email: 'ada@example.com',
      emailVerified: true,
      createdAt: new Date(),
      updatedAt: new Date(),
    })
    await db
      .insert(organization)
      .values({ id: 'org1', name: 'Ada Org', slug: 'ada-org', createdAt: new Date() })
    await db.insert(polls).values({
      id: 'p'.repeat(12),
      organizationId: 'org1',
      createdBy: 'u1',
      type: 'options',
      title: 'Lunch',
      timezone: 'Europe/Oslo',
      createdAt: now,
      updatedAt: now,
    })
    await db.insert(pollOptions).values([
      { id: 'o1', pollId: 'p'.repeat(12), position: 0, kind: 'text', label: 'Pizza' },
      { id: 'o2', pollId: 'p'.repeat(12), position: 1, kind: 'text', label: 'Sushi' },
    ])
    await db
      .insert(participants)
      .values({ id: 'pa1', pollId: 'p'.repeat(12), name: 'Bob', createdAt: now, updatedAt: now })
    await db.insert(votes).values([
      { participantId: 'pa1', optionId: 'o1', answer: 'yes' },
      { participantId: 'pa1', optionId: 'o2', answer: 'no' },
    ])

    const loaded = await db.query.polls.findFirst({
      where: eq(polls.id, 'p'.repeat(12)),
      with: { options: true, participants: { with: { votes: true } } },
    })
    expect(loaded?.options).toHaveLength(2)
    expect(loaded?.participants[0]?.votes).toHaveLength(2)
    expect(loaded?.status).toBe('open')

    await db.delete(polls).where(eq(polls.id, 'p'.repeat(12)))
    expect(await db.select().from(votes)).toHaveLength(0)
    expect(await db.select().from(pollOptions)).toHaveLength(0)
  })

  it('inserts a signup poll with signupMaxClaims and an option with capacity, and reads them back', async () => {
    const db = createDb(env.DB)
    const now = new Date().toISOString()
    await db
      .insert(organization)
      .values({ id: 'org-signup', name: 'Signup Org', slug: 'signup-org', createdAt: new Date() })
    await db.insert(polls).values({
      id: 's'.repeat(12),
      organizationId: 'org-signup',
      createdBy: null,
      type: 'signup',
      title: 'Bring a dish',
      timezone: 'Europe/Oslo',
      signupMaxClaims: 3,
      createdAt: now,
      updatedAt: now,
    })
    await db.insert(pollOptions).values([
      {
        id: 'so1',
        pollId: 's'.repeat(12),
        position: 0,
        kind: 'text',
        label: 'Salad',
        capacity: 5,
      },
    ])

    const loaded = await db.query.polls.findFirst({
      where: eq(polls.id, 's'.repeat(12)),
      with: { options: true },
    })
    expect(loaded?.type).toBe('signup')
    expect(loaded?.signupMaxClaims).toBe(3)
    expect(loaded?.options[0]?.capacity).toBe(5)
  })

  it('inserts a booking page and a booking, and reads them back with relations', async () => {
    const db = createDb(env.DB)
    const now = new Date().toISOString()
    await db.insert(user).values({
      id: 'u-booking-1',
      name: 'Grace',
      email: 'grace@example.com',
      emailVerified: true,
      createdAt: new Date(),
      updatedAt: new Date(),
    })
    await db
      .insert(organization)
      .values({ id: 'org-booking-1', name: 'Grace Org', slug: 'grace', createdAt: new Date() })
    await db.insert(bookingPages).values({
      id: 'bp1',
      organizationId: 'org-booking-1',
      createdBy: 'u-booking-1',
      memberUserId: 'u-booking-1',
      slug: 'intro-call',
      title: '15 min intro',
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      bufferBeforeMin: 0,
      bufferAfterMin: 0,
      minNoticeMin: 120,
      maxDaysAhead: 60,
      availability: JSON.stringify({ '1': [{ start: '09:00', end: '17:00' }] }),
      createdAt: now,
      updatedAt: now,
    })
    await db.insert(bookings).values({
      id: 'bk1',
      pageId: 'bp1',
      startAt: '2026-01-05T09:00:00.000Z',
      endAt: '2026-01-05T09:30:00.000Z',
      visitorName: 'Bob',
      visitorEmail: 'bob@example.com',
      visitorTimezone: 'Europe/Oslo',
      manageTokenHash: 'hashed-token',
      createdAt: now,
      updatedAt: now,
    })

    const loadedPage = await db.query.bookingPages.findFirst({
      where: eq(bookingPages.id, 'bp1'),
      with: { organization: true, member: true, bookings: true },
    })
    expect(loadedPage?.organization.slug).toBe('grace')
    expect(loadedPage?.member?.id).toBe('u-booking-1')
    expect(loadedPage?.status).toBe('active')
    expect(loadedPage?.googleSync).toBe(false)
    expect(loadedPage?.reminders).toBe(true)
    expect(loadedPage?.bookings).toHaveLength(1)
    expect(loadedPage?.bookings[0]?.status).toBe('confirmed')

    const loadedBooking = await db.query.bookings.findFirst({
      where: eq(bookings.id, 'bk1'),
      with: { page: true },
    })
    expect(loadedBooking?.page.slug).toBe('intro-call')

    await db.delete(bookingPages).where(eq(bookingPages.id, 'bp1'))
    expect(await db.select().from(bookings).where(eq(bookings.pageId, 'bp1'))).toHaveLength(0)
  })

  it('rejects a second booking page with the same (organizationId, slug)', async () => {
    const db = createDb(env.DB)
    const now = new Date().toISOString()
    await db
      .insert(organization)
      .values({ id: 'org-booking-2', name: 'Hedy Org', slug: 'hedy-org', createdAt: new Date() })
    const page = {
      organizationId: 'org-booking-2',
      slug: 'dup-slug',
      title: 'A page',
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      bufferBeforeMin: 0,
      bufferAfterMin: 0,
      minNoticeMin: 120,
      maxDaysAhead: 60,
      availability: '{}',
      createdAt: now,
      updatedAt: now,
    }
    await db.insert(bookingPages).values({ id: 'bp-dup-1', ...page })
    await expect(db.insert(bookingPages).values({ id: 'bp-dup-2', ...page })).rejects.toThrow()
  })

  it('rejects a second organization with the same slug', async () => {
    const db = createDb(env.DB)
    await db
      .insert(organization)
      .values({ id: 'org-handle-1', name: 'Ida Org', slug: 'shared-slug', createdAt: new Date() })
    await expect(
      db.insert(organization).values({
        id: 'org-handle-2',
        name: 'Iris Org',
        slug: 'shared-slug',
        createdAt: new Date(),
      }),
    ).rejects.toThrow()
  })
})
