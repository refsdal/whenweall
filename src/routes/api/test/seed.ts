import { createFileRoute } from '@tanstack/react-router'
import { env } from 'cloudflare:workers'
import { getAuth } from '#/server/auth/auth'
import { getDb } from '#/server/db/client'
import * as pollService from '#/server/polls/service'

type SeedBody = { email?: string; name?: string; password?: string; withPoll?: boolean }

/** Two datetime options, a couple of days out, so the seeded poll looks like a real one. */
function samplePollOptions(): { kind: 'datetime'; startAt: string; endAt: string }[] {
  const base = new Date()
  base.setUTCHours(16, 30, 0, 0)
  return [2, 3].map((offset) => {
    const start = new Date(base.getTime() + offset * 24 * 60 * 60 * 1000)
    const end = new Date(start.getTime() + 90 * 60 * 1000)
    return { kind: 'datetime', startAt: start.toISOString(), endAt: end.toISOString() }
  })
}

/**
 * Test-only route used by Playwright to create a verified user without going through the real
 * email-verification flow, and — with `withPoll` — a poll owned by that user. Gated so it can
 * never be reachable in production even if ENABLE_TEST_ROUTES is accidentally left set.
 */
export const Route = createFileRoute('/api/test/seed')({
  server: {
    handlers: {
      POST: async ({ request }) => {
        const appEnv: string = env.APP_ENV
        if (appEnv === 'production' || env.ENABLE_TEST_ROUTES !== 'true') {
          return new Response('Not found', { status: 404 })
        }

        const body = (await request.json().catch(() => ({}))) as SeedBody
        const email = body.email ?? `test-${crypto.randomUUID()}@example.com`
        const name = body.name ?? 'Test User'
        const password = body.password ?? 'correct horse battery staple'

        const signUp = await getAuth().api.signUpEmail({
          body: { name, email, password, locale: 'en' },
          headers: new Headers({ 'x-captcha-response': 'test' }),
        })

        await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()

        let pollId: string | null = null
        if (body.withPoll) {
          const created = await pollService.createPoll(getDb(), signUp.user.id, {
            type: 'datetime',
            title: 'Seeded test poll',
            description: 'Created by the test seed route.',
            timezone: 'Europe/Oslo',
            options: samplePollOptions(),
            allowComments: true,
            allowIfNeedBe: true,
            requireParticipantEmail: false,
          })
          pollId = created.id
        }

        return Response.json({ email, password, name, pollId })
      },
    },
  },
})
