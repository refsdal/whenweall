import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createAuth } from '#/server/auth/auth'

const captchaHeaders = new Headers({ 'x-captcha-response': 'test-token' })

describe('auth', () => {
  it('signs up (unverified), blocks sign-in until verified, then signs in', async () => {
    const auth = createAuth({ d1: env.DB, env })
    const signUp = await auth.api.signUpEmail({
      body: {
        name: 'Ada',
        email: 'ada@example.com',
        password: 'correct horse battery',
        locale: 'nb',
      },
      headers: captchaHeaders,
    })
    expect(signUp.user.email).toBe('ada@example.com')
    await expect(
      auth.api.signInEmail({
        body: { email: 'ada@example.com', password: 'correct horse battery' },
        headers: captchaHeaders,
      }),
    ).rejects.toMatchObject({ status: 'FORBIDDEN' })
    await env.DB.prepare('update user set email_verified = 1 where email = ?')
      .bind('ada@example.com')
      .run()
    const signIn = await auth.api.signInEmail({
      body: { email: 'ada@example.com', password: 'correct horse battery' },
      headers: captchaHeaders,
      asResponse: true,
    })
    expect(signIn.headers.get('set-cookie')).toContain('better-auth.session_token')
  })

  it('registers the captcha and passkey plugins', () => {
    const auth = createAuth({ d1: env.DB, env })
    expect(auth.options.plugins?.some((p) => p.id === 'captcha')).toBe(true)
    expect(auth.options.plugins?.some((p) => p.id === 'passkey')).toBe(true)
  })

  it('protects /send-verification-email with captcha, alongside the other auth endpoints', () => {
    const auth = createAuth({ d1: env.DB, env })
    const found = auth.options.plugins?.find((p) => p.id === 'captcha')
    // `.options` is the plugin's own construction options and is introspectable at runtime (the
    // server function objects built by `createServerFn` are not — see
    // test/server-functions.workers.test.ts for that manifest-based approach instead). The
    // plugin union's other members (passkey, tanstack-start-cookies) don't share this shape, so
    // this is cast through `unknown` rather than narrowed with a type predicate.
    const captchaPlugin = found as unknown as { options?: { endpoints?: string[] } } | undefined
    expect(captchaPlugin?.options?.endpoints).toEqual(
      expect.arrayContaining([
        '/sign-up/email',
        '/sign-in/email',
        '/request-password-reset',
        '/send-verification-email',
      ]),
    )
  })
})
