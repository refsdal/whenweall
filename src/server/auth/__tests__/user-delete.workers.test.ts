import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { bookingPages, member, organization, polls, user } from '#/server/db/schema'
import { createAuth } from '#/server/auth/auth'
import { createPoll } from '#/server/polls/service'
import { createPage } from '#/server/bookings/pages'

const authEnv = { ...env, APP_ENV: 'test' } as never
const captchaHeaders = new Headers({ 'x-captcha-response': 'test-token' })

/**
 * Signs a user up, marks them verified (bypassing the email-verification flow, same as
 * `personal-org.workers.test.ts`), and signs them in — returning their id, their auto-created
 * personal org id, and a session cookie for authenticated `auth.api.*` calls.
 */
async function signUpVerifiedWithOrg(
  auth: ReturnType<typeof createAuth>,
  name: string,
  password: string,
): Promise<{ userId: string; orgId: string; cookie: string }> {
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

  return { userId: signUp.user.id, orgId: m!.organizationId, cookie }
}

describe('deleting a user', () => {
  it("deletes the user's sole-owned personal org, and its polls/booking pages with it", async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const password = 'password-123456'
    const { userId, orgId, cookie } = await signUpVerifiedWithOrg(auth, 'Solo Owner', password)

    const db = createDb(env.DB)
    const { id: pollId } = await createPoll(
      db,
      { organizationId: orgId, createdBy: userId },
      {
        type: 'options',
        title: 'Lunch spot',
        timezone: 'Europe/Oslo',
        options: [{ kind: 'text', label: 'Pizza' }],
      },
    )
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: userId },
      {
        slug: 'intro-call',
        title: 'Intro call',
        timezone: 'Europe/Oslo',
        slotDurationMin: 30,
        bufferBeforeMin: 0,
        bufferAfterMin: 0,
        minNoticeMin: 0,
        maxDaysAhead: 60,
        availability: {},
        googleSync: false,
        reminders: true,
      },
    )

    await auth.api.deleteUser({
      headers: new Headers({ ...Object.fromEntries(captchaHeaders), cookie }),
      body: { password },
    })

    expect(await db.query.user.findFirst({ where: eq(user.id, userId) })).toBeUndefined()
    expect(
      await db.query.organization.findFirst({ where: eq(organization.id, orgId) }),
    ).toBeUndefined()
    expect(await db.query.polls.findFirst({ where: eq(polls.id, pollId) })).toBeUndefined()
    expect(
      await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) }),
    ).toBeUndefined()
  })

  it('keeps an org and its content when another owner remains, removing only the departing member row', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const password = 'password-123456'
    const {
      userId: ownerAId,
      orgId,
      cookie: cookieA,
    } = await signUpVerifiedWithOrg(auth, 'Owner A', password)
    // ownerB gets their own personal org too (irrelevant here) — only ownerB's id is needed to
    // add them as a second owner of ownerA's org below.
    const { userId: ownerBId } = await signUpVerifiedWithOrg(auth, 'Owner B', password)

    const db = createDb(env.DB)
    await db.insert(member).values({
      id: crypto.randomUUID(),
      organizationId: orgId,
      userId: ownerBId,
      role: 'owner',
      createdAt: new Date(),
    })

    const { id: pollId } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerAId },
      {
        type: 'options',
        title: 'Lunch spot',
        timezone: 'Europe/Oslo',
        options: [{ kind: 'text', label: 'Pizza' }],
      },
    )
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerAId },
      {
        slug: 'intro-call',
        title: 'Intro call',
        timezone: 'Europe/Oslo',
        slotDurationMin: 30,
        bufferBeforeMin: 0,
        bufferAfterMin: 0,
        minNoticeMin: 0,
        maxDaysAhead: 60,
        availability: {},
        googleSync: false,
        reminders: true,
      },
    )

    await auth.api.deleteUser({
      headers: new Headers({ ...Object.fromEntries(captchaHeaders), cookie: cookieA }),
      body: { password },
    })

    expect(await db.query.user.findFirst({ where: eq(user.id, ownerAId) })).toBeUndefined()
    expect(
      await db.query.organization.findFirst({ where: eq(organization.id, orgId) }),
    ).toBeTruthy()
    expect(await db.query.polls.findFirst({ where: eq(polls.id, pollId) })).toBeTruthy()
    expect(
      await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) }),
    ).toBeTruthy()

    const remainingMembers = await db.query.member.findMany({
      where: eq(member.organizationId, orgId),
    })
    expect(remainingMembers.map((m) => m.userId)).toEqual([ownerBId])
  })

  it('promotes the oldest remaining member to owner (rather than deleting the org) when the sole owner leaves but a plain member is still around', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const password = 'password-123456'
    const {
      userId: ownerId,
      orgId,
      cookie,
    } = await signUpVerifiedWithOrg(auth, 'Sole Owner', password)
    const { userId: memberId } = await signUpVerifiedWithOrg(auth, 'Plain Member', password)

    const db = createDb(env.DB)
    await db.insert(member).values({
      id: crypto.randomUUID(),
      organizationId: orgId,
      userId: memberId,
      role: 'member',
      createdAt: new Date(),
    })

    const { id: pollId } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'options',
        title: 'Lunch spot',
        timezone: 'Europe/Oslo',
        options: [{ kind: 'text', label: 'Pizza' }],
      },
    )
    const { id: pageId } = await createPage(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        slug: 'intro-call-promote',
        title: 'Intro call',
        timezone: 'Europe/Oslo',
        slotDurationMin: 30,
        bufferBeforeMin: 0,
        bufferAfterMin: 0,
        minNoticeMin: 0,
        maxDaysAhead: 60,
        availability: {},
        googleSync: false,
        reminders: true,
      },
    )

    await auth.api.deleteUser({
      headers: new Headers({ ...Object.fromEntries(captchaHeaders), cookie }),
      body: { password },
    })

    expect(await db.query.user.findFirst({ where: eq(user.id, ownerId) })).toBeUndefined()
    expect(
      await db.query.organization.findFirst({ where: eq(organization.id, orgId) }),
    ).toBeTruthy()
    expect(await db.query.polls.findFirst({ where: eq(polls.id, pollId) })).toBeTruthy()
    expect(
      await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) }),
    ).toBeTruthy()

    const remaining = await db.query.member.findFirst({ where: eq(member.organizationId, orgId) })
    expect(remaining?.userId).toBe(memberId)
    expect(remaining?.role).toBe('owner')
  })
})
