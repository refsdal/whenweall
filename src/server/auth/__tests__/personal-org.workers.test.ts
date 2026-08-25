import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { member, organization, user } from '#/server/db/schema'
import { createAuth } from '#/server/auth/auth'

const authEnv = { ...env, APP_ENV: 'test' } as never

describe('personal organization on signup', () => {
  it('creates an org + owner membership and scopes the session to it', async () => {
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
})
