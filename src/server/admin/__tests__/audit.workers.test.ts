import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { eq } from 'drizzle-orm'
import { createDb } from '#/server/db/client'
import { user } from '#/server/db/schema'
import { getAuth } from '#/server/auth/auth'
import * as auditModule from '#/server/admin/audit'
import { recordAdminAction } from '#/server/admin/audit'
import { newId } from '#/lib/ids'
import { makeUser } from '../../../../test/helpers'

/**
 * Signs a user up and returns the session cookie for them.
 *
 * `auth.api.*` called as a plain function deliberately skips the hook pipeline (see
 * `dispatchAuthEndpoint`'s doc comment in better-auth), which is what lets this bypass the
 * captcha plugin during setup. The assertion below then drives the HTTP handler, where hooks
 * *do* run.
 */
async function signedInCookie(email: string, role: 'user' | 'staff' = 'user'): Promise<string> {
  if (email !== email.toLowerCase())
    throw new Error('email must be lower case; Better-Auth normalises it on write')
  const auth = getAuth()
  const db = createDb(env.DB)
  const password = 'correct-horse-battery-staple'

  await auth.api.signUpEmail({ body: { name: 'Staff Person', email, password } })
  // Both before signing in: the session is minted from the user row as it stands at that
  // moment, so a role granted afterwards would not be reflected in this session.
  await db.update(user).set({ emailVerified: true, role }).where(eq(user.email, email))

  const res = await auth.api.signInEmail({ body: { email, password }, asResponse: true })
  const setCookie = res.headers.get('set-cookie')
  if (!setCookie) throw new Error('no session cookie returned from sign-in')
  return setCookie.split(';')[0]!
}

describe('recordAdminAction', () => {
  it('writes an auditable row', async () => {
    const db = createDb(env.DB)
    const { id, email } = await makeUser(db)

    await recordAdminAction(db, {
      actorUserId: id,
      actorEmail: email,
      action: 'set-user-password',
      targetType: 'user',
      targetId: 'target-9',
      reason: 'ticket 12',
      metadata: { fields: ['password'] },
    })

    const row = (await db.query.adminAuditLog.findMany()).find((r) => r.targetId === 'target-9')!
    expect(row.action).toBe('set-user-password')
    expect(row.actorEmail).toBe(email)
    expect(row.reason).toBe('ticket 12')
    expect(JSON.parse(row.metadata!)).toEqual({ fields: ['password'] })
  })

  // The trail is the only thing separating legitimate support from misuse, so it must not be
  // erasable from inside the application.
  it('exports no way to change or remove a recorded action', () => {
    const mutators = Object.keys(auditModule).filter((k) => /update|delete|remove|clear/i.test(k))
    expect(mutators).toEqual([])
  })
})

describe('the audit choke point', () => {
  // The assertion the whole design rests on. `/api/auth/admin/*` is reachable directly by any
  // staff user with curl, so auditing inside our own server functions would be trivially
  // bypassable. This drives the raw HTTP endpoint and expects a row anyway.
  it('records an action invoked through the raw HTTP endpoint, not just through our UI', async () => {
    const db = createDb(env.DB)
    const staffEmail = `staff-${newId().toLowerCase()}@example.com`
    const cookie = await signedInCookie(staffEmail, 'staff')

    const { id: targetId } = await makeUser(db)

    const res = await getAuth().handler(
      new Request('http://localhost:3000/api/auth/admin/ban-user', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          cookie,
          // Better-Auth rejects state-changing requests without an Origin matching its baseURL
          // (MISSING_OR_NULL_ORIGIN) — its CSRF protection, and worth exercising here rather than
          // bypassing, since this test is standing in for a real cross-origin caller.
          origin: 'http://localhost:3000',
          'x-admin-reason': 'abuse report 7',
        },
        body: JSON.stringify({ userId: targetId }),
      }),
    )
    expect(res.status).toBe(200)

    const rows = await db.query.adminAuditLog.findMany()
    const row = rows.find((r) => r.action === 'ban-user' && r.targetId === targetId)
    expect(row, 'a raw endpoint call left no audit row').toBeDefined()
    expect(row!.actorEmail).toBe(staffEmail)
    expect(row!.reason).toBe('abuse report 7')
  })

  it('does not audit read-only admin endpoints', async () => {
    const db = createDb(env.DB)
    const staffEmail = `staff-${newId().toLowerCase()}@example.com`
    const cookie = await signedInCookie(staffEmail, 'staff')

    const before = (await db.query.adminAuditLog.findMany()).length

    await getAuth().handler(
      new Request('http://localhost:3000/api/auth/admin/list-users?limit=1', {
        method: 'GET',
        headers: { cookie, origin: 'http://localhost:3000' },
      }),
    )

    expect((await db.query.adminAuditLog.findMany()).length).toBe(before)
  })
})
