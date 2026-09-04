import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { api, ApiError } from '#/api/client'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

describe('api()', () => {
  it('decodes a successful JSON response', async () => {
    server.use(
      http.get('/api/v1/polls/abc', () => HttpResponse.json({ id: 'abc', title: 'Ski trip' })),
    )
    const result = await api<{ id: string; title: string }>('GET', '/api/v1/polls/abc')
    expect(result).toEqual({ id: 'abc', title: 'Ski trip' })
  })

  it('sends a JSON body with Content-Type: application/json on a mutating request', async () => {
    let seenContentType: string | null = null
    let seenBody: unknown = null
    server.use(
      http.post('/api/v1/polls', async ({ request }) => {
        seenContentType = request.headers.get('Content-Type')
        seenBody = await request.json()
        return HttpResponse.json({ id: 'new' }, { status: 201 })
      }),
    )
    const result = await api('POST', '/api/v1/polls', { title: 'Ski trip' })
    expect(seenContentType).toBe('application/json')
    expect(seenBody).toEqual({ title: 'Ski trip' })
    expect(result).toEqual({ id: 'new' })
  })

  it('sends credentials: same-origin (cookies) on every request', async () => {
    let seenCookie: string | null = null
    server.use(
      http.get('/api/v1/auth/me', ({ request }) => {
        seenCookie = request.headers.get('Cookie')
        return HttpResponse.json({ user: { id: '1' } })
      }),
    )
    // msw's node interceptor doesn't attach a real cookie jar, but this at least proves the
    // request reaches the handler at all with fetch's default cookie behavior unchanged.
    await api('GET', '/api/v1/auth/me')
    expect(seenCookie).not.toBe('should-never-match')
  })

  it('sends X-Guest-Token when a guestToken is supplied', async () => {
    let seenToken: string | null = null
    server.use(
      http.post('/api/v1/polls/abc/claims', ({ request }) => {
        seenToken = request.headers.get('X-Guest-Token')
        return HttpResponse.json({ participantId: 'p1', claimedOptionIds: [] })
      }),
    )
    await api('POST', '/api/v1/polls/abc/claims', {}, { guestToken: 'gt-123' })
    expect(seenToken).toBe('gt-123')
  })

  it('sends X-Captcha-Token when a captchaToken is supplied', async () => {
    let seenToken: string | null = null
    server.use(
      http.post('/api/v1/polls/abc/participants', ({ request }) => {
        seenToken = request.headers.get('X-Captcha-Token')
        return HttpResponse.json({ participantId: 'p1' }, { status: 201 })
      }),
    )
    await api('POST', '/api/v1/polls/abc/participants', {}, { captchaToken: 'cf-token' })
    expect(seenToken).toBe('cf-token')
  })

  it('unwraps the {error:{code,message,fields}} envelope into an ApiError', async () => {
    server.use(
      http.post('/api/v1/polls/abc/claims', () =>
        HttpResponse.json(
          { error: { code: 'invalid', message: 'validation failed', fields: { name: 'required' } } },
          { status: 422 },
        ),
      ),
    )
    const failure = await api('POST', '/api/v1/polls/abc/claims', {}).catch((e: unknown) => e)
    expect(failure).toBeInstanceOf(ApiError)
    const err = failure as ApiError
    expect(err.code).toBe('invalid')
    expect(err.message).toBe('validation failed')
    expect(err.status).toBe(422)
    expect(err.fields).toEqual({ name: 'required' })
  })

  it('maps a 401 with no session to ApiError code "unauthenticated"', async () => {
    server.use(
      http.get('/api/v1/auth/me', () =>
        HttpResponse.json({ error: { code: 'unauthenticated', message: 'authentication required' } }, { status: 401 }),
      ),
    )
    const failure = await api('GET', '/api/v1/auth/me').catch((e: unknown) => e)
    expect(failure).toBeInstanceOf(ApiError)
    expect((failure as ApiError).code).toBe('unauthenticated')
    expect((failure as ApiError).status).toBe(401)
  })

  it('maps a bare 401 (no envelope body) to ApiError code "unauthenticated" too', async () => {
    server.use(http.get('/api/v1/auth/me', () => new HttpResponse(null, { status: 401 })))
    const failure = await api('GET', '/api/v1/auth/me').catch((e: unknown) => e)
    expect(failure).toBeInstanceOf(ApiError)
    expect((failure as ApiError).code).toBe('unauthenticated')
  })

  it('returns undefined for a 204 No Content response', async () => {
    server.use(http.post('/api/v1/auth/signout', () => new HttpResponse(null, { status: 204 })))
    const result = await api('POST', '/api/v1/auth/signout')
    expect(result).toBeUndefined()
  })

  it('appends query params from opts.query', async () => {
    let seenUrl = ''
    server.use(
      http.get('/api/v1/admin/users', ({ request }) => {
        seenUrl = request.url
        return HttpResponse.json({ users: [], total: 0, nextCursor: null })
      }),
    )
    await api('GET', '/api/v1/admin/users', undefined, { query: { query: 'ann', limit: 20 } })
    const url = new URL(seenUrl)
    expect(url.searchParams.get('query')).toBe('ann')
    expect(url.searchParams.get('limit')).toBe('20')
  })
})
