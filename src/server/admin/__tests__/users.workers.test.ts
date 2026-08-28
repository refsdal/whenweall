import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { getAdminUserDetail, listAdminUsers } from '#/server/admin/users'
import * as adminFunctions from '#/server/admin/admin.functions'
import { SERVER_FN_MIDDLEWARE } from '#/server/admin/admin.functions'
import { requireStaffMiddleware } from '#/server/auth/staff'
import { makeBookingPage, makePoll, makeUser, makeUserWithOrg } from '../../../../test/helpers'
import { newId } from '#/lib/ids'

const db = () => createDb(env.DB)

describe('listAdminUsers', () => {
  it('finds a user by a fragment of their email, case-insensitively', async () => {
    const email = `findme-${newId().toLowerCase()}@example.com`
    await makeUser(db(), { email })

    const found = await listAdminUsers(db(), {
      search: email.slice(0, 14).toUpperCase(),
      limit: 50,
      offset: 0,
    })

    expect(found.users.some((u) => u.email === email)).toBe(true)
  })

  it('finds a user by name', async () => {
    const name = `Ada ${newId()}`
    await makeUser(db(), { name })

    const found = await listAdminUsers(db(), { search: name, limit: 50, offset: 0 })

    expect(found.users.some((u) => u.name === name)).toBe(true)
  })

  it('reports a total independent of the page size', async () => {
    await makeUser(db())
    await makeUser(db())

    const page = await listAdminUsers(db(), { limit: 1, offset: 0 })

    expect(page.users).toHaveLength(1)
    expect(page.total).toBeGreaterThanOrEqual(2)
  })

  // A support screen has no business shipping credential material to a browser.
  it('never returns password or token material', async () => {
    await makeUser(db())

    const { users } = await listAdminUsers(db(), { limit: 1, offset: 0 })

    expect(Object.keys(users[0]!).sort()).toEqual([
      'banned',
      'createdAt',
      'email',
      'emailVerified',
      'id',
      'name',
      'role',
    ])
  })
})

describe('getAdminUserDetail', () => {
  it('returns null for an unknown id rather than throwing', async () => {
    await expect(getAdminUserDetail(db(), 'nope-does-not-exist')).resolves.toBeNull()
  })

  it('includes the orgs the user belongs to and what they have created', async () => {
    const { userId, orgId } = await makeUserWithOrg(db())
    await makePoll(db(), { orgId, createdBy: userId })
    await makeBookingPage(db(), { orgId, createdBy: userId })

    const detail = (await getAdminUserDetail(db(), userId))!

    expect(detail.orgs.map((o) => o.id)).toContain(orgId)
    expect(detail.counts.polls).toBe(1)
    expect(detail.counts.bookingPages).toBe(1)
  })
})

// A route guard is navigation only; the server function is the real gate. This asserts the gate
// is actually attached — the failure mode is a function shipped without it, which no amount of
// UI testing would catch.
describe('admin server functions', () => {
  it.each(['fetchAdminStats', 'fetchAdminUsers', 'fetchAdminUserDetail'] as const)(
    '%s is gated behind requireStaffMiddleware',
    (name) => {
      expect(typeof adminFunctions[name]).toBe('function')
      expect(SERVER_FN_MIDDLEWARE[name]).toContain(requireStaffMiddleware)
    },
  )
})
