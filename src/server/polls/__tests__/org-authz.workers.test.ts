import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { requireSessionMiddleware } from '#/server/auth/middleware'
import { requireOrgMiddleware, type OrgRole } from '#/server/auth/org'
import { createPoll, requireManagedPoll } from '#/server/polls/service'
import { addOrgMember, makeUser, makeUserWithOrg } from '../../../../test/helpers'

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

  it("a signed-in user whose active org they aren't a member of is rejected with FORBIDDEN", async () => {
    const db = createDb(env.DB)
    const { orgId } = await makeUserWithOrg(db)
    const { id: strangerId } = await makeUser(db) // never added as a member of orgId

    const fakeSession = { user: { id: strangerId }, session: { activeOrganizationId: orgId } }
    await expect(
      invokeMiddlewareServer(requireOrgMiddleware, { session: fakeSession }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
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
