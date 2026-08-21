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
})
