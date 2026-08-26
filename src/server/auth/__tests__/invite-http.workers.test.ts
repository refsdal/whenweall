import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { invitation, member, session as sessionTable } from '#/server/db/schema'
import { createAuth } from '#/server/auth/auth'
import { makeSubscription } from '../../../../test/helpers'

const authEnv = { ...env, APP_ENV: 'test' } as never
const BASE = 'http://localhost:3000/api/auth'
const captchaHeaders = { 'x-captcha-response': 'test-token' }

/** Drives the REAL mounted `auth.handler(request)` HTTP path — not `auth.api.*` — because the
 * seat-gate `hooks.before` resolves the caller's session differently depending on how much of
 * the request context Better-Auth has already populated by the time the hook runs, and that can
 * differ between a direct `auth.api.*` call and a request that comes in as a raw `Request`
 * through the router (see this file's own tests below for why that matters). Mirrors
 * `signUpVerifiedWithOrg` in `invitations.workers.test.ts`, but every step goes through
 * `auth.handler` so the whole flow — including the invite call itself — sees exactly what
 * production sees. */
async function signUpVerifiedWithOrgViaHttp(
  auth: ReturnType<typeof createAuth>,
  name: string,
  password: string,
): Promise<{ userId: string; orgId: string; cookie: string }> {
  const email = `${name.toLowerCase().replace(/\s+/g, '-')}-${crypto.randomUUID()}@example.com`

  const signUpResponse = await auth.handler(
    new Request(`${BASE}/sign-up/email`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        origin: 'http://localhost:3000',
        ...captchaHeaders,
      },
      body: JSON.stringify({ email, password, name }),
    }),
  )
  if (!signUpResponse.ok) {
    throw new Error(`sign-up failed: ${signUpResponse.status} ${await signUpResponse.text()}`)
  }
  const { user } = (await signUpResponse.json()) as { user: { id: string } }

  await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()

  const signInResponse = await auth.handler(
    new Request(`${BASE}/sign-in/email`, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        origin: 'http://localhost:3000',
        ...captchaHeaders,
      },
      body: JSON.stringify({ email, password }),
    }),
  )
  if (!signInResponse.ok) {
    throw new Error(`sign-in failed: ${signInResponse.status} ${await signInResponse.text()}`)
  }
  const cookie = signInResponse.headers.get('set-cookie')!.split(';')[0]!

  const db = createDb(env.DB)
  const m = await db.query.member.findFirst({ where: eq(member.userId, user.id) })

  return { userId: user.id, orgId: m!.organizationId, cookie }
}

describe('POST /api/auth/organization/invite-member over the real HTTP handler (Phase 2 §3)', () => {
  it('lets a Premium org owner invite without organizationId in the body — the real client never sends one', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { orgId, cookie } = await signUpVerifiedWithOrgViaHttp(
      auth,
      'HTTP Premium Owner',
      'password-123456',
    )
    const db = createDb(env.DB)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'active' })

    const email = `invitee-${crypto.randomUUID()}@example.com`
    const response = await auth.handler(
      new Request(`${BASE}/organization/invite-member`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', origin: 'http://localhost:3000', cookie },
        body: JSON.stringify({ email, role: 'member' }),
      }),
    )

    expect(response.status, await response.clone().text()).toBe(200)
    const row = await db.query.invitation.findFirst({ where: eq(invitation.email, email) })
    expect(row?.status).toBe('pending')
    expect(row?.organizationId).toBe(orgId)
  })

  it('rejects a Free org owner inviting without organizationId in the body with UPGRADE_REQUIRED', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { cookie } = await signUpVerifiedWithOrgViaHttp(
      auth,
      'HTTP Free Owner',
      'password-123456',
    )

    const email = `invitee-${crypto.randomUUID()}@example.com`
    const response = await auth.handler(
      new Request(`${BASE}/organization/invite-member`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', origin: 'http://localhost:3000', cookie },
        body: JSON.stringify({ email, role: 'member' }),
      }),
    )

    expect(response.status).toBe(403)
    const body = (await response.json()) as { message?: string }
    expect(body.message).toBe('UPGRADE_REQUIRED')
  })

  it('falls through to the plugin’s own resolution — not UPGRADE_REQUIRED — when neither the body nor the session yields an org id', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { userId, cookie } = await signUpVerifiedWithOrgViaHttp(
      auth,
      'HTTP No Active Org',
      'password-123456',
    )
    const db = createDb(env.DB)
    // Simulates a signed-in caller whose session has no active org at all (e.g. it was cleared
    // out from under them) — the seat-gate hook must not misclassify this as "Free" and throw
    // UPGRADE_REQUIRED; it has no org id to check entitlements for in the first place, so it
    // must let the plugin's own `organizationId` resolution run and reject on its own terms.
    await db
      .update(sessionTable)
      .set({ activeOrganizationId: null })
      .where(eq(sessionTable.userId, userId))

    const email = `invitee-${crypto.randomUUID()}@example.com`
    const response = await auth.handler(
      new Request(`${BASE}/organization/invite-member`, {
        method: 'POST',
        headers: { 'content-type': 'application/json', origin: 'http://localhost:3000', cookie },
        body: JSON.stringify({ email, role: 'member' }),
      }),
    )

    expect(response.status).toBe(400)
    const body = (await response.json()) as { message?: string }
    expect(body.message).not.toBe('UPGRADE_REQUIRED')
  })
})
