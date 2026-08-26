import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { getAuth } from '#/server/auth/auth'
import { member } from '#/server/db/schema'
import { applyClaim } from '#/server/polls/claims'
import { getPollView } from '#/server/polls/service'
import {
  addOrgMember,
  makePoll,
  makeSignupPoll,
  makeUserWithOrg,
} from '../../../../../test/helpers'
import { rosterResponse } from '../roster[.]csv'

const captchaHeaders = { 'x-captcha-response': 'test-token' }

async function signedInCookie(email: string, password: string): Promise<string> {
  const res = await getAuth().api.signInEmail({
    body: { email, password },
    headers: new Headers(captchaHeaders),
    asResponse: true,
  })
  const setCookie = res.headers.get('set-cookie')!
  return setCookie.split(';')[0]!
}

/** A real signed-up user, whose personal organization was auto-created by Better-Auth's
 * `databaseHooks` (see `createPersonalOrganization`) — unlike `makeUserWithOrg`, which builds
 * both rows directly and never goes through a real session. */
async function makeVerifiedOwner(): Promise<{
  id: string
  email: string
  cookie: string
  orgId: string
}> {
  const email = `owner-${crypto.randomUUID()}@example.com`
  const password = 'correct horse battery staple'
  const signUp = await getAuth().api.signUpEmail({
    body: { name: 'Owner', email, password, locale: 'en' },
    headers: new Headers(captchaHeaders),
  })
  await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()
  const cookie = await signedInCookie(email, password)
  const db = createDb(env.DB)
  const m = await db.query.member.findFirst({ where: eq(member.userId, signUp.user.id) })
  return { id: signUp.user.id, email, cookie, orgId: m!.organizationId }
}

describe('GET /p/$id/roster.csv', () => {
  it('returns 400 for a malformed poll id, without touching the database', async () => {
    const res = await rosterResponse(new Request('https://x'), 'not-a-valid-id!')
    expect(res.status).toBe(400)
  })

  it('returns 404 for a well-formed id that does not exist', async () => {
    const res = await rosterResponse(new Request('https://x'), 'missing12345')
    expect(res.status).toBe(404)
  })

  it('returns 401 when there is no session', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )

    const res = await rosterResponse(new Request('https://x'), pollId)
    expect(res.status).toBe(401)
  })

  it('returns 404 when the session user is signed in with a different org entirely', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const other = await makeVerifiedOwner()

    const res = await rosterResponse(
      new Request('https://x', { headers: { cookie: other.cookie } }),
      pollId,
    )
    expect(res.status).toBe(404)
  })

  it('returns 403 when the session user is a same-org member who did not create the poll', async () => {
    const db = createDb(env.DB)
    const owner = await makeVerifiedOwner()
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId: owner.orgId, createdBy: owner.id },
      { capacities: [null] },
    )
    const other = await makeVerifiedOwner()
    await addOrgMember(db, owner.orgId, other.id)
    // `other`'s session was created against their own personal org (the "first org by
    // createdAt" the session.create.before hook picks) before they were added as a member of
    // `owner`'s org above — force their *active* org to `owner`'s so the request actually
    // exercises the same-org-but-wrong-role path, not the different-org one.
    await env.DB.prepare('update session set active_organization_id = ? where user_id = ?')
      .bind(owner.orgId, other.id)
      .run()

    const res = await rosterResponse(
      new Request('https://x', { headers: { cookie: other.cookie } }),
      pollId,
    )
    expect(res.status).toBe(403)
  })

  it('returns 400 for the poll owner when the poll is not a sign-up sheet', async () => {
    const db = createDb(env.DB)
    const owner = await makeVerifiedOwner()
    const { id: pollId } = await makePoll(db, { orgId: owner.orgId, createdBy: owner.id })

    const res = await rosterResponse(
      new Request('https://x', { headers: { cookie: owner.cookie } }),
      pollId,
    )
    expect(res.status).toBe(400)
  })

  it('returns 200 with the CSV for the poll owner', async () => {
    const db = createDb(env.DB)
    const owner = await makeVerifiedOwner()
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId: owner.orgId, createdBy: owner.id },
      { capacities: [null] },
    )
    const view = await getPollView(db, pollId, { userId: owner.id })
    await applyClaim(db, pollId, view!.options[0]!.id, {
      name: 'Alice',
      email: 'alice@example.com',
      userId: null,
    })

    const res = await rosterResponse(
      new Request('https://x', { headers: { cookie: owner.cookie } }),
      pollId,
    )
    expect(res.status).toBe(200)
    expect(res.headers.get('Content-Type')).toContain('text/csv')
    expect(res.headers.get('Content-Disposition')).toBe(
      `attachment; filename="whenweall-${pollId}-roster.csv"`,
    )
    const text = await res.text()
    expect(text).toContain('slot,capacity,claimed,participant,email')
    expect(text).toContain('Alice')
  })
})
