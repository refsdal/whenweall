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
})
