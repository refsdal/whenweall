import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { deletePoll } from '#/server/polls/service'
import { makePoll, makeUserWithOrg } from '../../../../../../test/helpers'
import { Route } from '../ws'

// `createFileRoute(...)` exposes the raw handler at `Route.options.server.handlers.GET` — same
// pattern (and rationale) as src/routes/p/$id/__tests__/calendar-ics.workers.test.ts.
const GET = (
  Route as unknown as {
    options: {
      server: {
        handlers: {
          GET: (ctx: { request: Request; params: { id: string } }) => Promise<Response>
        }
      }
    }
  }
).options.server.handlers.GET

function upgrade(id: string): Promise<Response> {
  return GET({
    request: new Request('https://x', { headers: { Upgrade: 'websocket' } }),
    params: { id },
  })
}

describe('GET /api/polls/$id/ws', () => {
  it('returns 426 for a non-websocket request, before validating the id', async () => {
    const res = await GET({ request: new Request('https://x'), params: { id: 'not-a-valid-id!' } })
    expect(res.status).toBe(426)
  })

  it('returns 400 for a malformed poll id on an upgrade request', async () => {
    const res = await upgrade('not-a-valid-id!')
    expect(res.status).toBe(400)
  })

  // The reason this route exists in its current form: `POLL_ROOM.getByName(id)` materialises a
  // durable object for *any* well-formed id, and `PollRoom.fetch` used to write storage on every
  // connection. Without this check, a script generating random 12-character ids creates unbounded
  // durable objects, each with persisted storage that nothing ever collects.
  it('returns 404 for a well-formed id that does not exist, without addressing a durable object', async () => {
    const res = await upgrade('missing12345')
    expect(res.status).toBe(404)
  })

  it('returns 404 for a soft-deleted poll', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })
    await deletePoll(db, pollId, { id: orgId, role: 'owner' }, userId)

    const res = await upgrade(pollId)
    expect(res.status).toBe(404)
  })

  it('upgrades for a poll that exists', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })

    const res = await upgrade(pollId)
    expect(res.status).toBe(101)
  })
})
