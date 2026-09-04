import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { getUsageStats, loadLandingStats } from '#/api/stats'
import { EMPTY_STATS } from '#/lib/stats-types'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const LIVE = {
  pollsFinalized: 12,
  pollsCreated: 40,
  responsesYes: 300,
  responsesIfNeedBe: 20,
  responsesNo: 8,
}

describe('getUsageStats', () => {
  it('reads GET /api/v1/stats', async () => {
    server.use(http.get('/api/v1/stats', () => HttpResponse.json(LIVE)))
    await expect(getUsageStats()).resolves.toEqual(LIVE)
  })
})

describe('loadLandingStats', () => {
  it('hands the loader real numbers on first paint', async () => {
    server.use(http.get('/api/v1/stats', () => HttpResponse.json(LIVE)))
    await expect(loadLandingStats()).resolves.toEqual({ stats: LIVE })
  })

  it('never blocks the landing page on a failed read — falls back to zeros', async () => {
    server.use(http.get('/api/v1/stats', () => HttpResponse.text('upstream down', { status: 502 })))
    await expect(loadLandingStats()).resolves.toEqual({ stats: EMPTY_STATS })
  })

  // Fix-round regression test for a whole-plan review finding (Plan B review, Minor #4): the
  // loader awaits this read with no timeout, so a struggling backend stalled first paint on the
  // marketing page indefinitely instead of degrading to zeros the way a hard error already did.
  // Passes a short timeoutMs (loadLandingStats's real caller always uses the 2s default) so the
  // test doesn't actually wait out the production timeout.
  it('degrades to zeros instead of hanging when the read never resolves', async () => {
    server.use(
      http.get(
        '/api/v1/stats',
        () => new Promise(() => {}), // never resolves — simulates a stuck backend
      ),
    )
    await expect(loadLandingStats(20)).resolves.toEqual({ stats: EMPTY_STATS })
  })
})
