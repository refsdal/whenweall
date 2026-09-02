import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { me, myOrgRoles } from '#/api/auth'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

describe('me()', () => {
  it('reads id and isStaff straight off the session transformer payload', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({
          user: {
            id: '42',
            email: 'ada@example.com',
            first_name: 'Ada',
            last_name: 'Lovelace',
            email_verified_at: '2026-01-01T00:00:00Z',
            isStaff: true,
          },
        }),
      ),
    )

    const user = await me()

    expect(user).toEqual({
      id: '42',
      name: 'Ada Lovelace',
      email: 'ada@example.com',
      emailVerified: true,
      isStaff: true,
    })
  })

  it('defaults isStaff to false when the payload omits it', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({ user: { id: '1', email: 'a@example.com' } }),
      ),
    )

    const user = await me()

    expect(user?.isStaff).toBe(false)
  })

  it('returns null for an anonymous caller (401)', async () => {
    server.use(http.get('/api/v1/auth/me', () => new HttpResponse(null, { status: 401 })))

    expect(await me()).toBeNull()
  })
})

describe('myOrgRoles()', () => {
  it("returns the caller's role names in the active organization", async () => {
    server.use(
      http.get('/api/v1/auth/organizations/me', () => HttpResponse.json({ roles: ['owner'] })),
    )

    expect(await myOrgRoles()).toEqual(['owner'])
  })

  it('returns [] when there is no active organization (403)', async () => {
    server.use(
      http.get('/api/v1/auth/organizations/me', () =>
        HttpResponse.json({ error: { code: 'no_active_org', message: 'no active org' } }, { status: 403 }),
      ),
    )

    expect(await myOrgRoles()).toEqual([])
  })
})
