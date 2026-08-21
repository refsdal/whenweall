import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { AppError } from '#/lib/errors'
import {
  createPage,
  deletePage,
  getOwnedPage,
  getPublicPage,
  listMyPages,
  setUserHandle,
  updatePage,
} from '#/server/bookings/pages'
import { makeBookingPage, makeBooking, makeUser } from '../../../../test/helpers'

const weekday = [{ start: '09:00', end: '17:00' }]

function baseInput(overrides?: Record<string, unknown>) {
  return {
    slug: 'intro-call',
    title: '15 min intro',
    timezone: 'Europe/Oslo',
    slotDurationMin: 30,
    bufferBeforeMin: 0,
    bufferAfterMin: 0,
    minNoticeMin: 0,
    maxDaysAhead: 60,
    availability: { '1': weekday, '2': weekday, '3': weekday, '4': weekday, '5': weekday },
    googleSync: false,
    reminders: true,
    ...overrides,
  } as Parameters<typeof createPage>[2]
}

describe('createPage', () => {
  it('creates a page and rejects a second one with the same (owner, slug)', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)

    const { id } = await createPage(db, ownerId, baseInput())
    expect(id).toBeTruthy()

    const view = await getOwnedPage(db, id, ownerId)
    expect(view.slug).toBe('intro-call')
    expect(view.status).toBe('active')
    expect(view.availability['1']).toEqual(weekday)

    await expect(createPage(db, ownerId, baseInput())).rejects.toMatchObject(
      new AppError('SLUG_TAKEN'),
    )
  })

  it('allows the same slug for two different owners', async () => {
    const db = createDb(env.DB)
    const { id: owner1 } = await makeUser(db)
    const { id: owner2 } = await makeUser(db)

    await createPage(db, owner1, baseInput())
    await expect(createPage(db, owner2, baseInput())).resolves.toBeTruthy()
  })

  it('allows reusing a slug after the page that held it is soft-deleted', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)

    const { id: firstId } = await createPage(db, ownerId, baseInput())
    await deletePage(db, firstId, ownerId)

    const { id: secondId } = await createPage(db, ownerId, baseInput())
    expect(secondId).toBeTruthy()
    expect(secondId).not.toBe(firstId)

    // Two live pages with the same (owner, slug) still collide.
    await expect(createPage(db, ownerId, baseInput())).rejects.toMatchObject(
      new AppError('SLUG_TAKEN'),
    )
  })
})

describe('updatePage', () => {
  it('updates fields and enforces NOT_FOUND/FORBIDDEN/SLUG_TAKEN', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: pageId } = await createPage(db, ownerId, baseInput())
    await createPage(db, ownerId, baseInput({ slug: 'other-slug' }))

    await updatePage(db, pageId, ownerId, { title: 'New title', status: 'paused' })
    const view = await getOwnedPage(db, pageId, ownerId)
    expect(view.title).toBe('New title')
    expect(view.status).toBe('paused')

    await expect(updatePage(db, pageId, otherId, { title: 'x' })).rejects.toMatchObject(
      new AppError('FORBIDDEN'),
    )
    await expect(updatePage(db, 'missing', ownerId, { title: 'x' })).rejects.toMatchObject(
      new AppError('NOT_FOUND'),
    )
    await expect(updatePage(db, pageId, ownerId, { slug: 'other-slug' })).rejects.toMatchObject(
      new AppError('SLUG_TAKEN'),
    )
  })
})

describe('deletePage', () => {
  it('soft-deletes so the page disappears from listMyPages and getOwnedPage', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await createPage(db, ownerId, baseInput())

    await deletePage(db, pageId, ownerId)

    await expect(getOwnedPage(db, pageId, ownerId)).rejects.toMatchObject(new AppError('NOT_FOUND'))
    expect(await listMyPages(db, ownerId)).toHaveLength(0)
  })
})

describe('listMyPages', () => {
  it('reports upcomingCount from confirmed future bookings', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId)

    const future = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString()
    const past = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString()
    await makeBooking(db, pageId, future)
    await makeBooking(db, pageId, past)
    await makeBooking(db, pageId, new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString(), {
      status: 'cancelled',
      cancelledBy: 'visitor',
    })

    const [summary] = await listMyPages(db, ownerId)
    expect(summary?.upcomingCount).toBe(1)
  })
})

describe('getPublicPage', () => {
  it('resolves by handle + slug and includes the owner name and status', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, { name: 'Grace' })
    await setUserHandle(db, ownerId, 'grace')
    await createPage(db, ownerId, baseInput())

    const page = await getPublicPage(db, 'grace', 'intro-call')
    expect(page?.title).toBe('15 min intro')
    expect(page?.owner).toEqual({ name: 'Grace' })
    expect(page?.status).toBe('active')
    // no owner id / email leak to the public view
    expect(page).not.toHaveProperty('ownerId')
    expect(page).not.toHaveProperty('email')
  })

  it('still returns a paused page (so the route can show a paused message)', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, { name: 'Hedy' })
    await setUserHandle(db, ownerId, 'hedy')
    const { id: pageId } = await createPage(db, ownerId, baseInput())
    await updatePage(db, pageId, ownerId, { status: 'paused' })

    const page = await getPublicPage(db, 'hedy', 'intro-call')
    expect(page?.status).toBe('paused')
  })

  it('returns null for a deleted page, unknown handle, or unknown slug', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db, { name: 'Ida' })
    await setUserHandle(db, ownerId, 'ida')
    const { id: pageId } = await createPage(db, ownerId, baseInput())

    expect(await getPublicPage(db, 'unknown-handle', 'intro-call')).toBeNull()
    expect(await getPublicPage(db, 'ida', 'unknown-slug')).toBeNull()

    await deletePage(db, pageId, ownerId)
    expect(await getPublicPage(db, 'ida', 'intro-call')).toBeNull()
  })
})

describe('setUserHandle', () => {
  it('sets the handle and rejects a second user taking the same one', async () => {
    const db = createDb(env.DB)
    const { id: user1 } = await makeUser(db)
    const { id: user2 } = await makeUser(db)

    await setUserHandle(db, user1, 'shared-handle')
    await expect(setUserHandle(db, user2, 'shared-handle')).rejects.toMatchObject(
      new AppError('HANDLE_TAKEN'),
    )
  })
})
