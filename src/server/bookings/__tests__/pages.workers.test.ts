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
  requireManagedPage,
  setOrgSlug,
  updatePage,
} from '#/server/bookings/pages'
import {
  addOrgMember,
  makeBookingPage,
  makeBooking,
  makeUser,
  makeUserWithOrg,
} from '../../../../test/helpers'

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
  it('creates a page and rejects a second one with the same (org, slug)', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const owner = { organizationId: orgId, createdBy: ownerId }

    const { id } = await createPage(db, owner, baseInput())
    expect(id).toBeTruthy()

    const view = await getOwnedPage(db, id, org, ownerId)
    expect(view.slug).toBe('intro-call')
    expect(view.status).toBe('active')
    expect(view.availability['1']).toEqual(weekday)

    await expect(createPage(db, owner, baseInput())).rejects.toMatchObject(
      new AppError('SLUG_TAKEN'),
    )
  })

  it('allows the same slug for two different orgs', async () => {
    const db = createDb(env.DB)
    const { userId: owner1, orgId: org1 } = await makeUserWithOrg(db)
    const { userId: owner2, orgId: org2 } = await makeUserWithOrg(db)

    await createPage(db, { organizationId: org1, createdBy: owner1 }, baseInput())
    await expect(
      createPage(db, { organizationId: org2, createdBy: owner2 }, baseInput()),
    ).resolves.toBeTruthy()
  })

  it('allows reusing a slug after the page that held it is soft-deleted', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const owner = { organizationId: orgId, createdBy: ownerId }

    const { id: firstId } = await createPage(db, owner, baseInput())
    await deletePage(db, firstId, org, ownerId)

    const { id: secondId } = await createPage(db, owner, baseInput())
    expect(secondId).toBeTruthy()
    expect(secondId).not.toBe(firstId)

    // Two live pages with the same (org, slug) still collide.
    await expect(createPage(db, owner, baseInput())).rejects.toMatchObject(
      new AppError('SLUG_TAKEN'),
    )
  })

  it('defaults memberUserId to createdBy, but an explicit memberUserId (or null) overrides it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: teammateId } = await makeUser(db)
    await addOrgMember(db, orgId, teammateId)

    const { id: defaultedId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      baseInput(),
    )
    const defaulted = await requireManagedPage(db, defaultedId, org, ownerId)
    expect(defaulted.memberUserId).toBe(ownerId)

    const { id: handedOffId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId, memberUserId: teammateId },
      baseInput({ slug: 'handed-off' }),
    )
    const handedOff = await requireManagedPage(db, handedOffId, org, ownerId)
    expect(handedOff.memberUserId).toBe(teammateId)

    const { id: noMemberId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId, memberUserId: null },
      baseInput({ slug: 'no-member' }),
    )
    const noMember = await requireManagedPage(db, noMemberId, org, ownerId)
    expect(noMember.memberUserId).toBeNull()
  })
})

describe('requireManagedPage', () => {
  it('throws NOT_FOUND when missing or in a different org, FORBIDDEN for a same-org non-manager', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: memberId } = await makeUser(db)
    await addOrgMember(db, orgId, memberId)
    const memberOrg = { id: orgId, role: 'member' as const }
    const { userId: otherId, orgId: otherOrgId } = await makeUserWithOrg(db)
    const otherOrg = { id: otherOrgId, role: 'owner' as const }
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      baseInput(),
    )

    await expect(requireManagedPage(db, 'missing', org, ownerId)).rejects.toMatchObject(
      new AppError('NOT_FOUND'),
    )
    await expect(requireManagedPage(db, pageId, otherOrg, otherId)).rejects.toMatchObject(
      new AppError('NOT_FOUND'),
    )
    await expect(requireManagedPage(db, pageId, memberOrg, memberId)).rejects.toMatchObject(
      new AppError('FORBIDDEN'),
    )
    await expect(requireManagedPage(db, pageId, org, ownerId)).resolves.toMatchObject({
      id: pageId,
    })
  })
})

describe('updatePage', () => {
  it('updates fields and enforces NOT_FOUND/FORBIDDEN/SLUG_TAKEN', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: memberId } = await makeUser(db)
    await addOrgMember(db, orgId, memberId)
    const memberOrg = { id: orgId, role: 'member' as const }
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      baseInput(),
    )
    await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      baseInput({ slug: 'other-slug' }),
    )

    await updatePage(db, pageId, org, ownerId, { title: 'New title', status: 'paused' })
    const view = await getOwnedPage(db, pageId, org, ownerId)
    expect(view.title).toBe('New title')
    expect(view.status).toBe('paused')

    await expect(updatePage(db, pageId, memberOrg, memberId, { title: 'x' })).rejects.toMatchObject(
      new AppError('FORBIDDEN'),
    )
    await expect(updatePage(db, 'missing', org, ownerId, { title: 'x' })).rejects.toMatchObject(
      new AppError('NOT_FOUND'),
    )
    await expect(
      updatePage(db, pageId, org, ownerId, { slug: 'other-slug' }),
    ).rejects.toMatchObject(new AppError('SLUG_TAKEN'))
  })
})

describe('deletePage', () => {
  it('soft-deletes so the page disappears from listMyPages and getOwnedPage', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      baseInput(),
    )

    await deletePage(db, pageId, org, ownerId)

    await expect(getOwnedPage(db, pageId, org, ownerId)).rejects.toMatchObject(
      new AppError('NOT_FOUND'),
    )
    expect(await listMyPages(db, orgId)).toHaveLength(0)
  })
})

describe('listMyPages', () => {
  it('reports upcomingCount from confirmed future bookings', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pageId } = await makeBookingPage(db, { orgId, createdBy: ownerId })

    const future = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString()
    const past = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString()
    await makeBooking(db, pageId, future)
    await makeBooking(db, pageId, past)
    await makeBooking(db, pageId, new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString(), {
      status: 'cancelled',
      cancelledBy: 'visitor',
    })

    const [summary] = await listMyPages(db, orgId)
    expect(summary?.upcomingCount).toBe(1)
  })
})

describe('getPublicPage', () => {
  it('resolves by handle (org slug) + page slug and includes the owner name and status', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db, { name: 'Grace' })
    await setOrgSlug(db, orgId, 'grace')
    await createPage(db, { organizationId: orgId, createdBy: ownerId }, baseInput())

    const page = await getPublicPage(db, 'grace', 'intro-call')
    expect(page?.title).toBe('15 min intro')
    expect(page?.owner).toEqual({ name: 'Test Org' })
    expect(page?.status).toBe('active')
    expect(page?.handle).toBe('grace')
    expect(page?.slug).toBe('intro-call')
    // no org id / email leak to the public view
    expect(page).not.toHaveProperty('organizationId')
    expect(page).not.toHaveProperty('email')
  })

  it('only exposes fields the public client actually uses (no availability/buffers/minNotice)', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db, { name: 'Iris' })
    await setOrgSlug(db, orgId, 'iris')
    await createPage(db, { organizationId: orgId, createdBy: ownerId }, baseInput())

    const page = await getPublicPage(db, 'iris', 'intro-call')
    expect(page && Object.keys(page).sort()).toEqual(
      [
        'id',
        'handle',
        'slug',
        'title',
        'description',
        'location',
        'timezone',
        'slotDurationMin',
        'maxDaysAhead',
        'status',
        'owner',
      ].sort(),
    )
  })

  it('still returns a paused page (so the route can show a paused message)', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db, { name: 'Hedy' })
    const org = { id: orgId, role: 'owner' as const }
    await setOrgSlug(db, orgId, 'hedy')
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      baseInput(),
    )
    await updatePage(db, pageId, org, ownerId, { status: 'paused' })

    const page = await getPublicPage(db, 'hedy', 'intro-call')
    expect(page?.status).toBe('paused')
  })

  it('returns null for a deleted page, unknown handle, or unknown slug', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db, { name: 'Ida' })
    const org = { id: orgId, role: 'owner' as const }
    await setOrgSlug(db, orgId, 'ida')
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      baseInput(),
    )

    expect(await getPublicPage(db, 'unknown-handle', 'intro-call')).toBeNull()
    expect(await getPublicPage(db, 'ida', 'unknown-slug')).toBeNull()

    await deletePage(db, pageId, org, ownerId)
    expect(await getPublicPage(db, 'ida', 'intro-call')).toBeNull()
  })
})

describe('setOrgSlug', () => {
  it('sets the org slug and rejects a second org taking the same one', async () => {
    const db = createDb(env.DB)
    const { orgId: org1 } = await makeUserWithOrg(db)
    const { orgId: org2 } = await makeUserWithOrg(db)

    await setOrgSlug(db, org1, 'shared-handle')
    await expect(setOrgSlug(db, org2, 'shared-handle')).rejects.toMatchObject(
      new AppError('HANDLE_TAKEN'),
    )
  })
})
