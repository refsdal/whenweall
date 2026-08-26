import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { member, organization, user } from '#/server/db/schema'
import { createAuth } from '#/server/auth/auth'
import { handleSchema } from '#/server/bookings/schemas'

const authEnv = { ...env, APP_ENV: 'test' } as never
const captchaHeaders = new Headers({ 'x-captcha-response': 'test-token' })

describe('personal organization on signup', () => {
  it('creates an org + owner membership', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const email = `org-hook-${crypto.randomUUID()}@example.com`
    await auth.api.signUpEmail({
      body: { email, password: 'password-123456', name: 'Kari Nordmann' },
    })
    const db = createDb(env.DB)
    const u = await db.query.user.findFirst({ where: eq(user.email, email) })
    expect(u).toBeTruthy()
    const m = await db.query.member.findFirst({ where: eq(member.userId, u!.id) })
    expect(m?.role).toBe('owner')
    const org = await db.query.organization.findFirst({
      where: eq(organization.id, m!.organizationId),
    })
    expect(org?.name).toBe('Kari Nordmann')
    expect(org?.slug).toMatch(/^kari-nordmann-[a-z0-9]{6}$/)
  })

  it('gives a user with a long, slug-heavy name an org slug that still passes handleSchema (regression: used to overflow its 30-char max)', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const email = `long-name-${crypto.randomUUID()}@example.com`
    await auth.api.signUpEmail({
      body: { email, password: 'password-123456', name: 'Christopher Alexander Ng' },
    })
    const db = createDb(env.DB)
    const u = await db.query.user.findFirst({ where: eq(user.email, email) })
    const m = await db.query.member.findFirst({ where: eq(member.userId, u!.id) })
    const org = await db.query.organization.findFirst({
      where: eq(organization.id, m!.organizationId),
    })

    expect(org?.slug.length).toBeGreaterThanOrEqual(24)
    expect(handleSchema.safeParse(org?.slug).success).toBe(true)
  })

  it('scopes a freshly created session to the personal org (session.create.before hook)', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const email = `org-session-${crypto.randomUUID()}@example.com`
    const password = 'password-123456'
    await auth.api.signUpEmail({
      body: { email, password, name: 'Ola Nordmann' },
      headers: captchaHeaders,
    })
    // signUpEmail doesn't sign the caller in while requireEmailVerification is on, so mark the
    // user verified and sign in explicitly to actually exercise session.create.before.
    await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()

    const db = createDb(env.DB)
    const u = await db.query.user.findFirst({ where: eq(user.email, email) })
    const m = await db.query.member.findFirst({ where: eq(member.userId, u!.id) })
    expect(m).toBeTruthy()

    const signIn = await auth.api.signInEmail({
      body: { email, password },
      headers: captchaHeaders,
      asResponse: true,
    })
    const cookie = signIn.headers.get('set-cookie')!.split(';')[0]!
    const result = await auth.api.getSession({ headers: new Headers({ cookie }) })

    expect(result?.session.activeOrganizationId).toBe(m!.organizationId)
  })
})
