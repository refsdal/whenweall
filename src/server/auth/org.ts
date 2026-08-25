import { createMiddleware } from '@tanstack/react-start'
import { and, eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { member } from '#/server/db/schema'
import { requireSessionMiddleware } from './middleware'
import { AppError } from '#/lib/errors'

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

export const requireOrgMiddleware = createMiddleware({ type: 'function' })
  .middleware([requireSessionMiddleware])
  .server(async ({ next, context }) => {
    const activeOrgId = (context.session.session as { activeOrganizationId?: string | null })
      .activeOrganizationId
    if (!activeOrgId) throw new AppError('UNAUTHORIZED')
    const membership = await getDb().query.member.findFirst({
      where: and(
        eq(member.organizationId, activeOrgId),
        eq(member.userId, context.session.user.id),
      ),
    })
    if (!membership) throw new AppError('FORBIDDEN')
    return next({ context: { org: { id: activeOrgId, role: membership.role as OrgRole } } })
  })
