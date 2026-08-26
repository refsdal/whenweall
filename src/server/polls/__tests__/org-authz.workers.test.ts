import { env } from 'cloudflare:workers'
import { and, eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { member, organization } from '#/server/db/schema'
import { requireSessionMiddleware } from '#/server/auth/middleware'
import { requireOrgMiddleware, type OrgRole } from '#/server/auth/org'
import { createPoll, requireManagedPoll } from '#/server/polls/service'
import { addOrgMember, makeOrg, makeUser, makeUserWithOrg } from '../../../../test/helpers'

/**
 * Directly invokes a `createMiddleware(...)` object's own `.server` function (available at
 * `middleware.options.server` — unlike a built `createServerFn(...)`, a middleware object does
 * not hide this behind an opaque RPC wrapper; see `FunctionMiddlewareWithTypes` in
 * `@tanstack/start-client-core`). This lets the authorization matrix below exercise the *actual*
 * `requireSessionMiddleware`/`requireOrgMiddleware` decision logic — not a re-implementation of
 * it — without booting a real request (which `sessionMiddleware`, further upstream, needs; see
 * `middleware.ts`'s own comment on why it can't run outside a real request context).
 */
async function invokeMiddlewareServer(
  middleware: { options: { server?: unknown } },
  context: unknown,
): Promise<unknown> {
  const server = middleware.options.server as (opts: Record<string, unknown>) => unknown
  return server({
    data: undefined,
    context,
    next: (opts?: { context?: unknown }) => ({
      context: { ...(context as object), ...((opts?.context as object) ?? {}) },
    }),
    method: 'POST',
    serverFnMeta: undefined,
    signal: new AbortController().signal,
  })
}

describe('org authorization matrix', () => {
  it('member creates content and can manage their own; a second member cannot (FORBIDDEN); an admin can; a different org gets NOT_FOUND', async () => {
    const db = createDb(env.DB)
    const { orgId } = await makeUserWithOrg(db)

    const { id: memberId } = await makeUser(db)
    await addOrgMember(db, orgId, memberId, 'member')
    const memberOrg = { id: orgId, role: 'member' as OrgRole }

    const { id: pollId } = await createPoll(
      db,
      { organizationId: orgId, createdBy: memberId },
      {
        type: 'options',
        title: 'Lunch spot',
        timezone: 'Europe/Oslo',
        options: [{ kind: 'text', label: 'Pizza' }],
      },
    )

    // member manages their own poll ✓
    await expect(requireManagedPoll(db, pollId, memberOrg, memberId)).resolves.toMatchObject({
      id: pollId,
    })

    // a second member, who didn't create it, cannot — FORBIDDEN
    const { id: secondMemberId } = await makeUser(db)
    await addOrgMember(db, orgId, secondMemberId, 'member')
    await expect(requireManagedPoll(db, pollId, memberOrg, secondMemberId)).rejects.toMatchObject({
      code: 'FORBIDDEN',
    })

    // an admin can manage it despite not creating it
    const { id: adminId } = await makeUser(db)
    await addOrgMember(db, orgId, adminId, 'admin')
    const adminOrg = { id: orgId, role: 'admin' as OrgRole }
    await expect(requireManagedPoll(db, pollId, adminOrg, adminId)).resolves.toMatchObject({
      id: pollId,
    })

    // a user from an entirely different org gets NOT_FOUND — no leaking that the poll exists
    // outside their own org
    const { userId: otherOrgUserId, orgId: otherOrgId } = await makeUserWithOrg(db)
    const otherOrg = { id: otherOrgId, role: 'owner' as OrgRole }
    await expect(requireManagedPoll(db, pollId, otherOrg, otherOrgUserId)).rejects.toMatchObject({
      code: 'NOT_FOUND',
    })
  })

  it('unauthenticated (no session at all) is rejected with UNAUTHORIZED', async () => {
    await expect(
      invokeMiddlewareServer(requireSessionMiddleware, { session: null }),
    ).rejects.toMatchObject({ code: 'UNAUTHORIZED' })
  })

  it('a signed-in user with no active org is rejected with UNAUTHORIZED', async () => {
    const fakeSession = { user: { id: 'u_no_org' }, session: { activeOrganizationId: null } }
    await expect(
      invokeMiddlewareServer(requireOrgMiddleware, { session: fakeSession }),
    ).rejects.toMatchObject({ code: 'UNAUTHORIZED' })
  })

  it("a signed-in user whose active org they aren't a member of falls back to a lazily-created personal org (they have no memberships of their own)", async () => {
    const db = createDb(env.DB)
    const { orgId } = await makeUserWithOrg(db)
    // Never added as a member of orgId, and has no org of their own either — same shape as a
    // dangling activeOrganizationId (e.g. their membership was removed after the session was
    // issued): the middleware must not lock them out, but it must not silently drop them into
    // someone else's org either.
    const { id: strangerId, email: strangerEmail } = await makeUser(db, { name: 'Stranger' })

    const fakeSession = {
      user: { id: strangerId, name: 'Stranger', email: strangerEmail },
      session: { activeOrganizationId: orgId },
    }
    const result = (await invokeMiddlewareServer(requireOrgMiddleware, {
      session: fakeSession,
    })) as { context: { org: { id: string; role: OrgRole } } }

    expect(result.context.org.id).not.toBe(orgId)
    expect(result.context.org.role).toBe('owner')
    const created = await db.query.organization.findFirst({
      where: eq(organization.id, result.context.org.id),
    })
    expect(created?.name).toBe('Stranger')
    const createdMembership = await db.query.member.findFirst({
      where: and(eq(member.organizationId, result.context.org.id), eq(member.userId, strangerId)),
    })
    expect(createdMembership?.role).toBe('owner')
  })

  it('falls back to the oldest remaining membership when the active org id is dangling (the membership row was deleted) instead of FORBIDDEN', async () => {
    const db = createDb(env.DB)
    const { userId, orgId: oldestOrgId } = await makeUserWithOrg(db)
    const { id: otherOrgId } = await makeOrg(db, userId)

    // Force explicit, unambiguous createdAt ordering (independent of real-clock timing between
    // the two makeOrg calls above).
    await db
      .update(member)
      .set({ createdAt: new Date('2020-01-01') })
      .where(and(eq(member.organizationId, oldestOrgId), eq(member.userId, userId)))
    await db
      .update(member)
      .set({ createdAt: new Date('2020-06-01') })
      .where(and(eq(member.organizationId, otherOrgId), eq(member.userId, userId)))

    // Simulate a dangling activeOrganizationId: the session still points at otherOrgId, but that
    // membership row is gone (e.g. the user was removed from it after the session was issued).
    await db
      .delete(member)
      .where(and(eq(member.organizationId, otherOrgId), eq(member.userId, userId)))

    const fakeSession = {
      user: { id: userId, name: 'irrelevant', email: 'irrelevant@example.com' },
      session: { activeOrganizationId: otherOrgId },
    }
    const result = (await invokeMiddlewareServer(requireOrgMiddleware, {
      session: fakeSession,
    })) as { context: { org: { id: string; role: OrgRole } } }

    expect(result.context.org).toEqual({ id: oldestOrgId, role: 'owner' })
  })

  it('a signed-in member of their active org gets context.org with the right id and role', async () => {
    const db = createDb(env.DB)
    const { orgId } = await makeUserWithOrg(db)
    const { id: memberId } = await makeUser(db)
    await addOrgMember(db, orgId, memberId, 'admin')

    const fakeSession = { user: { id: memberId }, session: { activeOrganizationId: orgId } }
    const result = (await invokeMiddlewareServer(requireOrgMiddleware, {
      session: fakeSession,
    })) as { context: { org: { id: string; role: OrgRole } } }

    expect(result.context.org).toEqual({ id: orgId, role: 'admin' })
  })
})
