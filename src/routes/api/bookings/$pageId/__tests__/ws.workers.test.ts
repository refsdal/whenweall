import { describe, expect, it } from 'vitest'
import { newId } from '#/lib/ids'
import { Route } from '../ws'

// `createFileRoute(...)` exposes the raw handler at `Route.options.server.handlers.GET` — see
// the same pattern (and its rationale) in
// src/routes/p/$id/__tests__/calendar-ics.workers.test.ts.
const GET = (
  Route as unknown as {
    options: {
      server: {
        handlers: {
          GET: (ctx: { request: Request; params: { pageId: string } }) => Promise<Response>
        }
      }
    }
  }
).options.server.handlers.GET

describe('GET /api/bookings/$pageId/ws', () => {
  it('returns 426 for a non-websocket request, before validating the id', async () => {
    const res = await GET({
      request: new Request('https://x'),
      params: { pageId: 'not-a-valid-id!' },
    })
    expect(res.status).toBe(426)
  })

  it('returns 400 for a malformed page id on an upgrade request', async () => {
    const res = await GET({
      request: new Request('https://x', { headers: { Upgrade: 'websocket' } }),
      params: { pageId: 'not-a-valid-id!' },
    })
    expect(res.status).toBe(400)
  })

  it('accepts a well-formed 16-char page id and routes past the id check (to the Durable Object)', async () => {
    const res = await GET({
      request: new Request('https://x', { headers: { Upgrade: 'websocket' } }),
      params: { pageId: newId() },
    })
    // Past the 400 check — whatever the DO/websocket upgrade itself does in this harness, it is
    // not "Bad id".
    expect(res.status).not.toBe(400)
  })
})
