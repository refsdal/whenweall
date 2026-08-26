import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { finalizePoll, getPollView } from '#/server/polls/service'
import { makePoll, makeUserWithOrg } from '../../../../../test/helpers'
import { Route } from '../calendar[.]ics'

// `createFileRoute(...)` exposes the raw handler at `Route.options.server.handlers.GET`, so it
// can be invoked directly with a minimal `{ params }` — no need to boot the router or a real HTTP
// server, unlike a `createServerFn` (see test/server-functions.workers.test.ts for why that one
// needs a manifest instead). Cast through `unknown` rather than reaching into `Route.options`'s
// own generic `Constrain<...>` type, which TypeScript can't narrow back down to a plain function
// from outside the route definition's own type-inference scope.
const GET = (
  Route as unknown as {
    options: {
      server: { handlers: { GET: (ctx: { params: { id: string } }) => Promise<Response> } }
    }
  }
).options.server.handlers.GET

describe('GET /p/$id/calendar.ics', () => {
  it('returns 400 for a malformed poll id, without touching the database', async () => {
    const res = await GET({ params: { id: 'not-a-valid-id!' } })
    expect(res.status).toBe(400)
  })

  it('returns 404 for a well-formed id that does not exist', async () => {
    const res = await GET({ params: { id: 'missing12345' } })
    expect(res.status).toBe(404)
  })

  it('returns the ics calendar for a finalized poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId }, { title: 'Team sync' })
    const view = await getPollView(db, pollId, { userId: ownerId })
    await finalizePoll(db, pollId, org, ownerId, view!.options[0]!.id)

    const res = await GET({ params: { id: pollId } })
    expect(res.status).toBe(200)
    expect(res.headers.get('Content-Type')).toContain('text/calendar')
    expect(await res.text()).toContain('BEGIN:VCALENDAR')
  })
})
