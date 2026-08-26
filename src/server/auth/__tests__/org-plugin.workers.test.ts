import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createAuth } from '#/server/auth/auth'

const authEnv = { ...env, APP_ENV: 'test' } as never
const captchaHeaders = new Headers({ 'x-captcha-response': 'test-token' })

/** Signs a user up, marks them verified (bypassing the email-verification flow), and signs them
 * in — returning a session cookie for authenticated `auth.api.*` calls. Same pattern as
 * `user-delete.workers.test.ts`/`personal-org.workers.test.ts`. */
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

describe('organization plugin guardrails (Phase 1: no invitations yet, bounded slugs/orgs)', () => {
  it('rejects org creation with a slug that fails handleSchema, straight through the Better-Auth endpoint', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { cookie } = await signUpVerified(auth, 'Org Creator', 'password-123456')

    await expect(
      auth.api.createOrganization({
        headers: new Headers({ cookie }),
        body: { name: 'Too Short', slug: 'ab' }, // fails handleSchema.min(3), passes the plugin's own min(1)
      }),
    ).rejects.toMatchObject({ status: 'BAD_REQUEST' })
  })

  it('rejects updating an organization to a slug that fails handleSchema', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { cookie } = await signUpVerified(auth, 'Org Updater', 'password-123456')

    await expect(
      auth.api.updateOrganization({
        headers: new Headers({ cookie }),
        body: { data: { slug: 'AB CAPS!' } }, // fails handleSchema's lowercase/hyphen regex
      }),
    ).rejects.toMatchObject({ status: 'BAD_REQUEST' })
  })

  it('rejects org creation once the user is at organizationLimit (5, including their personal org)', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { cookie } = await signUpVerified(auth, 'Org Limit', 'password-123456')

    // Signup already created 1 (personal) org; 4 more brings the user to the limit of 5.
    for (let i = 0; i < 4; i++) {
      await auth.api.createOrganization({
        headers: new Headers({ cookie }),
        body: { name: `Extra org ${i}`, slug: `org-lim-${crypto.randomUUID().slice(0, 8)}` },
      })
    }

    await expect(
      auth.api.createOrganization({
        headers: new Headers({ cookie }),
        body: { name: 'One too many', slug: `org-lim-${crypto.randomUUID().slice(0, 8)}` },
      }),
    ).rejects.toMatchObject({ status: 'FORBIDDEN' })
  })

  it('rejects inviting a member — Phase 1 has no seats to invite into and no accept route yet', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { cookie } = await signUpVerified(auth, 'Org Inviter', 'password-123456')

    await expect(
      auth.api.createInvitation({
        headers: new Headers({ cookie }),
        body: { email: `invitee-${crypto.randomUUID()}@example.com`, role: 'member' },
      }),
    ).rejects.toMatchObject({ status: 'FORBIDDEN' })
  })
})
