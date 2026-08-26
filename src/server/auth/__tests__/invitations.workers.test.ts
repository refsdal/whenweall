import { env } from 'cloudflare:workers'
import { and, eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { invitation, member } from '#/server/db/schema'
import { createAuth } from '#/server/auth/auth'
import { addOrgMember, makeSubscription, makeUser } from '../../../../test/helpers'

const authEnv = { ...env, APP_ENV: 'test' } as never
const captchaHeaders = new Headers({ 'x-captcha-response': 'test-token' })

/** Same shape as `user-delete.workers.test.ts`'s helper of the same name: signs a user up, marks
 * them verified, signs them in, and reports their auto-created personal org id alongside the
 * session cookie and email — invitations gate on that org's entitlements. */
async function signUpVerifiedWithOrg(
  auth: ReturnType<typeof createAuth>,
  name: string,
  password: string,
): Promise<{ userId: string; orgId: string; email: string; cookie: string }> {
  const email = `${name.toLowerCase().replace(/\s+/g, '-')}-${crypto.randomUUID()}@example.com`
  const signUp = await auth.api.signUpEmail({
    body: { email, password, name },
    headers: captchaHeaders,
  })
  await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()

  const db = createDb(env.DB)
  const m = await db.query.member.findFirst({ where: eq(member.userId, signUp.user.id) })

  const signIn = await auth.api.signInEmail({
    body: { email, password },
    headers: captchaHeaders,
    asResponse: true,
  })
  const cookie = signIn.headers.get('set-cookie')!.split(';')[0]!

  return { userId: signUp.user.id, orgId: m!.organizationId, email, cookie }
}

describe('seat-gated invitations (Phase 2 §3)', () => {
  it('rejects inviting on a Free org (no active subscription) with UPGRADE_REQUIRED', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { cookie } = await signUpVerifiedWithOrg(auth, 'Free Owner', 'password-123456')

    await expect(
      auth.api.createInvitation({
        headers: new Headers({ cookie }),
        body: { email: `invitee-${crypto.randomUUID()}@example.com`, role: 'member' },
      }),
    ).rejects.toMatchObject({ status: 'FORBIDDEN', body: { message: 'UPGRADE_REQUIRED' } })
  })

  it('creates the invitation (and sends the email) on a Premium org under the seat cap', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { orgId, cookie } = await signUpVerifiedWithOrg(auth, 'Premium Owner', 'password-123456')
    const db = createDb(env.DB)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'active' })

    const email = `invitee-${crypto.randomUUID()}@example.com`
    const result = await auth.api.createInvitation({
      headers: new Headers({ cookie }),
      body: { email, role: 'member' },
    })

    expect(result.email).toBe(email)
    const row = await db.query.invitation.findFirst({ where: eq(invitation.email, email) })
    expect(row?.status).toBe('pending')
    expect(row?.organizationId).toBe(orgId)
  })

  it('rejects inviting once members + pending invitations reach the Premium seat cap (10)', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { orgId, cookie } = await signUpVerifiedWithOrg(auth, 'Cap Owner', 'password-123456')
    const db = createDb(env.DB)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'active' })

    // Owner is member #1; 8 filler members bring the org to 9 members.
    for (let i = 0; i < 8; i++) {
      const filler = await makeUser(db)
      await addOrgMember(db, orgId, filler.id)
    }

    // 9 members + 0 pending (< 10) — this invite is allowed and becomes the 10th occupied seat.
    await auth.api.createInvitation({
      headers: new Headers({ cookie }),
      body: { email: `invitee-1-${crypto.randomUUID()}@example.com`, role: 'member' },
    })

    // 9 members + 1 pending (>= 10) — the next invite is blocked.
    await expect(
      auth.api.createInvitation({
        headers: new Headers({ cookie }),
        body: { email: `invitee-2-${crypto.randomUUID()}@example.com`, role: 'member' },
      }),
    ).rejects.toMatchObject({ status: 'FORBIDDEN', body: { message: 'SEAT_LIMIT_REACHED' } })
  })

  it('accepting an invitation creates a member row and marks the invitation accepted', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const { orgId, cookie: ownerCookie } = await signUpVerifiedWithOrg(
      auth,
      'Accept Owner',
      'password-123456',
    )
    const db = createDb(env.DB)
    await makeSubscription(db, orgId, { plan: 'premium', status: 'active' })

    const {
      userId: inviteeId,
      email: inviteeEmail,
      cookie: inviteeCookie,
    } = await signUpVerifiedWithOrg(auth, 'Accept Invitee', 'password-123456')

    const created = await auth.api.createInvitation({
      headers: new Headers({ cookie: ownerCookie }),
      body: { email: inviteeEmail, role: 'member' },
    })

    await auth.api.acceptInvitation({
      headers: new Headers({ cookie: inviteeCookie }),
      body: { invitationId: created.id },
    })

    const memberRow = await db.query.member.findFirst({
      where: and(eq(member.organizationId, orgId), eq(member.userId, inviteeId)),
    })
    expect(memberRow).toBeTruthy()

    const invitationRow = await db.query.invitation.findFirst({
      where: eq(invitation.id, created.id),
    })
    expect(invitationRow?.status).toBe('accepted')
  })
})
