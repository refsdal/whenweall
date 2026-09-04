import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { finalizePoll } from '#/api/polls'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

describe('finalizePoll', () => {
  it('posts the option and returns the server-reported { sent } count', async () => {
    let seenBody: unknown = null
    server.use(
      http.post('/api/v1/polls/abc/finalize', async ({ request }) => {
        seenBody = await request.json()
        return HttpResponse.json({ sent: 3 })
      }),
    )

    const result = await finalizePoll('abc', 'opt-1')

    expect(seenBody).toEqual({ optionId: 'opt-1' })
    expect(result).toEqual({ sent: 3 })
  })
})
