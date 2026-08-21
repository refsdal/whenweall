import { createFileRoute } from '@tanstack/react-router'
import { env } from 'cloudflare:workers'
import { getAuth } from '#/server/auth/auth'

type SeedBody = { email?: string; name?: string; password?: string }

/**
 * Test-only route used by Playwright to create a verified user without going through the real
 * email-verification flow. Gated so it can never be reachable in production even if
 * ENABLE_TEST_ROUTES is accidentally left set.
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

        await getAuth().api.signUpEmail({
          body: { name, email, password, locale: 'en' },
          headers: new Headers({ 'x-captcha-response': 'test' }),
        })

        await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()

        return Response.json({ email, password, name })
      },
    },
  },
})
