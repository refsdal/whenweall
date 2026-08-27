import { describe, expect, it } from 'vitest'
import { Route } from '../health'

// `createFileRoute(...)` exposes the raw handler at `Route.options.server.handlers.GET` — same
// pattern (and rationale) as src/routes/p/$id/__tests__/calendar-ics.workers.test.ts.
const GET = (
  Route as unknown as { options: { server: { handlers: { GET: () => Promise<Response> } } } }
).options.server.handlers.GET

describe('GET /api/health', () => {
  it('reports ok when the database answers', async () => {
    const res = await GET()
    expect(res.status).toBe(200)
    await expect(res.json()).resolves.toEqual({ ok: true })
  })

  it('is not cacheable and not indexable', async () => {
    const res = await GET()
    expect(res.headers.get('cache-control')).toBe('no-store')
    expect(res.headers.get('x-robots-tag')).toBe('noindex')
  })

  it('says nothing about the deployment beyond up/down', async () => {
    const res = await GET()
    // A probe is unauthenticated, so its body must not become a free deployment fingerprint.
    expect(Object.keys((await res.json()) as object)).toEqual(['ok'])
  })
})
