import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import {
  acceptInvitation,
  deleteOwnAccount,
  listOrganizations,
  me,
  myOrgRoles,
  requestPasswordReset,
  signInWithCredential,
  signUpWithCredential,
  switchOrganization,
  updateProfile,
} from '#/api/auth'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

describe('me()', () => {
  it('reads the session transformer payload: id, name, locale, emailVerified, hasPassword, isStaff', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({
          user: {
            id: '42',
            email: 'ada@example.com',
            first_name: 'Ada',
            last_name: 'Lovelace',
            name: 'Ada Lovelace',
            locale: 'nb',
            emailVerified: true,
            hasPassword: true,
            isStaff: true,
          },
        }),
      ),
    )

    expect(await me()).toEqual({
      id: '42',
      name: 'Ada Lovelace',
      email: 'ada@example.com',
      emailVerified: true,
      locale: 'nb',
      hasPassword: true,
      isStaff: true,
    })
  })

  it('falls back to composed name, en locale and email_verified_at when the new fields are absent', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({
          user: { id: '1', email: 'a@example.com', first_name: 'A', email_verified_at: null },
        }),
      ),
    )

    const user = await me()

    expect(user?.name).toBe('A')
    expect(user?.locale).toBe('en')
    expect(user?.emailVerified).toBe(false)
    expect(user?.hasPassword).toBe(false)
    expect(user?.isStaff).toBe(false)
  })

  it('returns null for an anonymous caller (401)', async () => {
    server.use(http.get('/api/v1/auth/me', () => new HttpResponse(null, { status: 401 })))

    expect(await me()).toBeNull()
  })

  it('returns null for a locked account (403 from the auth mount guard) instead of throwing', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({ error: { code: 'forbidden', message: 'account is locked' } }, { status: 403 }),
      ),
    )

    expect(await me()).toBeNull()
  })
})

describe('captcha token forwarding', () => {
  it('sends X-Captcha-Token on sign-in only when a token is given', async () => {
    const seen: (string | null)[] = []
    server.use(
      http.post('/api/v1/auth/signin/credential', ({ request }) => {
        seen.push(request.headers.get('X-Captcha-Token'))
        return HttpResponse.json({ user: { id: '1', email: 'a@example.com' } })
      }),
    )

    await signInWithCredential('a@example.com', 'pw', 'tok')
    await signInWithCredential('a@example.com', 'pw', null)
    await signInWithCredential('a@example.com', 'pw')

    expect(seen).toEqual(['tok', null, null])
  })

  it('sign-up sends name, locale and the captcha token', async () => {
    let body: unknown
    let header: string | null = null
    server.use(
      http.post('/api/v1/auth/signup/credential', async ({ request }) => {
        body = await request.json()
        header = request.headers.get('X-Captcha-Token')
        return HttpResponse.json({ user: { id: '1', email: 'a@example.com' } })
      }),
    )

    await signUpWithCredential('a@example.com', 'pw', 'Ada Lovelace', 'tok')

    expect(body).toEqual({ email: 'a@example.com', password: 'pw', name: 'Ada Lovelace', locale: 'en' })
    expect(header).toBe('tok')
  })

  it('password-reset request forwards the token', async () => {
    let header: string | null = null
    server.use(
      http.post('/api/v1/auth/passwords/request-reset', ({ request }) => {
        header = request.headers.get('X-Captcha-Token')
        return HttpResponse.json('ok')
      }),
    )

    await requestPasswordReset('a@example.com', 'tok')

    expect(header).toBe('tok')
  })
})

describe('account routes', () => {
  it('updateProfile PATCHes /api/v1/me with only the given fields', async () => {
    let body: unknown
    server.use(
      http.patch('/api/v1/me', async ({ request }) => {
        body = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )

    await updateProfile({ locale: 'nb' })

    expect(body).toEqual({ locale: 'nb' })
  })

  it('deleteOwnAccount DELETEs /api/v1/me with the password when given', async () => {
    let body: unknown = 'unset'
    server.use(
      http.delete('/api/v1/me', async ({ request }) => {
        body = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )

    await deleteOwnAccount('hunter2hunter2')

    expect(body).toEqual({ password: 'hunter2hunter2' })
  })

  it('listOrganizations and switchOrganization use the /api/v1/me organization routes', async () => {
    let switched: unknown
    server.use(
      http.get('/api/v1/me/organizations', () =>
        HttpResponse.json([{ id: '7', name: 'Team', slug: 'team', active: false }]),
      ),
      http.post('/api/v1/me/active-organization', async ({ request }) => {
        switched = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )

    expect(await listOrganizations()).toEqual([{ id: '7', name: 'Team', slug: 'team', active: false }])
    await switchOrganization('7')
    expect(switched).toEqual({ orgId: '7' })
  })

  it('acceptInvitation reads the org slug by token, accepts, and returns the slug', async () => {
    const calls: string[] = []
    server.use(
      http.get('/api/v1/auth/organizations/invitations/token/tok-1', () => {
        calls.push('read')
        return HttpResponse.json({ email: 'a@example.com', organization: { name: 'Team', slug: 'team' } })
      }),
      http.post('/api/v1/auth/organizations/invitations/respond', async ({ request }) => {
        calls.push('respond:' + JSON.stringify(await request.json()))
        return HttpResponse.json({ status: 'accepted' })
      }),
    )

    expect(await acceptInvitation('tok-1')).toEqual({ orgSlug: 'team' })
    expect(calls).toEqual(['read', 'respond:{"token":"tok-1","response":"accept"}'])
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
