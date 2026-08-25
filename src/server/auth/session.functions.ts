import { createServerFn } from '@tanstack/react-start'
import { and, eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { member } from '#/server/db/schema'
import { sessionMiddleware } from './middleware'
import type { OrgRole } from './org'

export const getSession = createServerFn({ method: 'GET' })
  .middleware([sessionMiddleware])
  .handler(async ({ context }) => {
    const s = context.session
    if (!s) return null

    const activeOrgId = (s.session as { activeOrganizationId?: string | null }).activeOrganizationId
    const membership = activeOrgId
      ? await getDb().query.member.findFirst({
          where: and(eq(member.organizationId, activeOrgId), eq(member.userId, s.user.id)),
          with: { organization: true },
        })
      : null

    return {
      user: {
        id: s.user.id,
        name: s.user.name,
        email: s.user.email,
        image: s.user.image ?? null,
        locale: (s.user as { locale?: string }).locale ?? null,
        // Better-Auth `additionalFields`, so it rides along on the session user; the booking
        // UI needs it to render `/book/<handle>/<slug>` links.
        handle: (s.user as { handle?: string }).handle ?? null,
      },
      org: membership
        ? {
            id: membership.organizationId,
            slug: membership.organization.slug,
            name: membership.organization.name,
            role: membership.role as OrgRole,
          }
        : null,
    }
  })

export type ClientSession = Awaited<ReturnType<typeof getSession>>
