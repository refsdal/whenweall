// Note: the task brief called for `fetchMock` from `cloudflare:test`, matching the older
// `@cloudflare/vitest-pool-workers` API. This project's installed `@cloudflare/vitest-plugin@1.x`
// does not export `fetchMock` at all; its documented outbound-request mocking mechanism is MSW
// via `@msw/cloudflare` (see cloudflare/workers-sdk fixtures/vitest-plugin-examples/request-mocking).
// Both packages were added as dev dependencies for this.
import { setupNetwork } from '@msw/cloudflare'
import { http, HttpResponse } from 'msw'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { verifyTurnstile } from '#/server/http/turnstile'

const network = setupNetwork()

beforeAll(() => network.enable())
afterEach(() => network.resetHandlers())
afterAll(() => network.disable())

const SITEVERIFY_URL = 'https://challenges.cloudflare.com/turnstile/v0/siteverify'

describe('verifyTurnstile', () => {
  it('returns true when siteverify succeeds', async () => {
    network.use(http.post(SITEVERIFY_URL, () => HttpResponse.json({ success: true })))

    await expect(verifyTurnstile('tok')).resolves.toBe(true)
  })

  it('returns false when siteverify fails', async () => {
    network.use(http.post(SITEVERIFY_URL, () => HttpResponse.json({ success: false })))

    await expect(verifyTurnstile('tok')).resolves.toBe(false)
  })

  it('returns false without making a request when token is missing', async () => {
    await expect(verifyTurnstile(undefined)).resolves.toBe(false)
  })
})
