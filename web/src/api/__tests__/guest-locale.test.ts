import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { addParticipant, claimSlot, updateParticipant } from '#/api/polls'
import { bookSlot } from '#/api/bookings'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

// Every guest-facing mutation carries the visitor's UI locale so the Go side can localize the
// mail it sends them (internal/polls and internal/bookings already accept `locale`). In vitest
// paraglide resolves the base locale, "en".
describe('guest forms send locale', () => {
  it('addParticipant', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.post('/api/v1/polls/p1/participants', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ participantId: 'x' })
      }),
    )
    await addParticipant('p1', { name: 'Ada', answers: {} })
    expect(body.locale).toBe('en')
  })

  it('updateParticipant', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.patch('/api/v1/polls/p1/participants/pa1', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await updateParticipant('p1', 'pa1', { answers: {} })
    expect(body.locale).toBe('en')
  })

  it('claimSlot', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.post('/api/v1/polls/p1/claims', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ participantId: 'x', claimedOptionIds: [] })
      }),
    )
    await claimSlot('p1', { optionId: 'o1', name: 'Ada' })
    expect(body.locale).toBe('en')
  })

  it('bookSlot', async () => {
    let body: Record<string, unknown> = {}
    server.use(
      http.post('/api/v1/book/ada/intro/bookings', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ booking: { id: 'b1' }, manageToken: 't' })
      }),
    )
    await bookSlot('ada', 'intro', { startAt: '2026-09-15T07:00:00.000Z', name: 'Ada', email: 'a@example.com', timezone: 'Europe/Oslo' })
    expect(body.locale).toBe('en')
  })
})
