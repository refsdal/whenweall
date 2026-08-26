import { createMiddleware } from '@tanstack/react-start'
import { and, eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import { member } from '#/server/db/schema'
import { requireSessionMiddleware } from './middleware'
import { AppError } from '#/lib/errors'
import { createPersonalOrganization } from './personal-org'

export type OrgRole = 'owner' | 'admin' | 'member'

/** Creator manages their own content; admin/owner manage everything in the org (spec §1). */
export function canManageContent(
  org: { role: OrgRole },
  userId: string,
  createdBy: string | null,
): boolean {
  return (
    org.role === 'owner' || org.role === 'admin' || (createdBy !== null && createdBy === userId)
  )
}

/** Only the org's owner may change identity-affecting org settings, like its public slug (spec
 * §1: unlike ordinary content, the org's own slug isn't "manage everything" territory for
 * admins). */
export function requireOwnerRole(role: OrgRole): void {
  if (role !== 'owner') throw new AppError('FORBIDDEN')
}

/** The bare minimum `resolveActiveOrg` needs from a session: enough to look up memberships
 * (`user.id`) and enough to lazily create a personal org if it comes to that
 * (`user.name`/`user.email` — see `createPersonalOrganization`). */
type ResolvableSession = {
  user: { id: string; name: string; email: string }
  session: { activeOrganizationId?: string | null }
}

/**
 * Resolves a session's active org, recovering from a *dangling* `activeOrganizationId` — one set
 * on the session but no longer backed by a live membership row (e.g. the user was removed from
 * that org, or it was deleted) after the session was issued. Rather than lock the user out:
 *  1. their oldest remaining membership (by `member.createdAt`) is used instead, if they have one;
 *  2. failing that (no memberships left at all), their personal org is lazily recreated.
 *
 * Returns `null` only when `session.session.activeOrganizationId` was never set to begin with —
 * callers decide what that means for them (`requireOrgMiddleware` treats it as UNAUTHORIZED;
 * `buildClientSession` just reports no active org).
 */
export async function resolveActiveOrg(
  db: Db,
  session: ResolvableSession,
): Promise<{ id: string; slug: string; name: string; role: OrgRole } | null> {
  const activeOrgId = session.session.activeOrganizationId
  if (!activeOrgId) return null

  const membership = await db.query.member.findFirst({
    where: and(eq(member.organizationId, activeOrgId), eq(member.userId, session.user.id)),
    with: { organization: true },
  })
  if (membership) {
    return {
      id: membership.organizationId,
      slug: membership.organization.slug,
      name: membership.organization.name,
      role: membership.role as OrgRole,
    }
  }

  // Dangling: fall back to the user's oldest remaining membership rather than reject them.
  const oldest = await db.query.member.findFirst({
    where: eq(member.userId, session.user.id),
    orderBy: (m, { asc }) => [asc(m.createdAt)],
    with: { organization: true },
  })
  if (oldest) {
    return {
      id: oldest.organizationId,
      slug: oldest.organization.slug,
      name: oldest.organization.name,
      role: oldest.role as OrgRole,
    }
  }

  // No memberships left at all: lazily recreate a personal org so the user always lands
  // somewhere usable.
  const { orgId, slug } = await createPersonalOrganization(db, session.user)
  return { id: orgId, slug, name: session.user.name, role: 'owner' }
}

export const requireOrgMiddleware = createMiddleware({ type: 'function' })
  .middleware([requireSessionMiddleware])
  .server(async ({ next, context }) => {
    const org = await resolveActiveOrg(getDb(), context.session)
    if (!org) throw new AppError('UNAUTHORIZED')
    return next({ context: { org: { id: org.id, role: org.role } } })
  })
