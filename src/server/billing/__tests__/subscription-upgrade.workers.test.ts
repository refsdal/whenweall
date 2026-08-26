import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createAuth } from '#/server/auth/auth'

const authEnv = { ...env, APP_ENV: 'test' } as never
const captchaHeaders = new Headers({ 'x-captcha-response': 'test-token' })

/** Mirrors `signUpVerifiedWithOrg` in `invitations.workers.test.ts`: signs a user up, marks them
 * verified and signs them in, returning just the session cookie — this suite only needs an
 * authenticated caller, not their org id. */
async function signUpVerified(
  auth: ReturnType<typeof createAuth>,
  name: string,
  password: string,
): Promise<{ cookie: string }> {
  const email = `${name.toLowerCase().replace(/\s+/g, '-')}-${crypto.randomUUID()}@example.com`
  await auth.api.signUpEmail({ body: { email, password, name }, headers: captchaHeaders })
  await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()

  const signIn = await auth.api.signInEmail({
    body: { email, password },
    headers: captchaHeaders,
    asResponse: true,
  })
  const cookie = signIn.headers.get('set-cookie')!.split(';')[0]!
  return { cookie }
}

describe('POST /subscription/upgrade requires an explicit referenceId (Phase 2 §5)', () => {
  it('rejects an upgrade with no referenceId — @better-auth/stripe would otherwise default to the session user id and skip authorizeReference entirely', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { cookie } = await signUpVerified(auth, 'Upgrade Caller', 'password-123456')

    await expect(
      auth.api.upgradeSubscription({
        headers: new Headers({ cookie }),
        body: { plan: 'premium' },
      }),
    ).rejects.toMatchObject({ status: 'BAD_REQUEST', body: { message: 'referenceId required' } })
  })
})
