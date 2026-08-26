import { createServerFn } from '@tanstack/react-start'
import { and, eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import { member } from '#/server/db/schema'
import { sessionMiddleware } from './middleware'
import type { Session } from './auth'
import type { OrgRole } from './org'

export type ClientSessionPayload = {
  user: {
    id: string
    name: string
    email: string
    image: string | null
    locale: string | null
  }
  org: { id: string; slug: string; name: string; role: OrgRole } | null
}

/**
 * Extracted from `getSession`'s `.handler(...)` so it can be exercised directly in a workers test
 * with a hand-built `session` object and a real DB — a built `createServerFn(...)` object can't be
 * invoked directly (see `test/server-functions.workers.test.ts`), so the logic behind it needs to
 * live somewhere testable on its own, same pattern as `rosterResponse`/`bookingIcsResponse`.
 */
export async function buildClientSession(
  db: Db,
  session: Session | null,
): Promise<ClientSessionPayload | null> {
  const s = session
  if (!s) return null

  const activeOrgId = (s.session as { activeOrganizationId?: string | null }).activeOrganizationId
  const membership = activeOrgId
    ? await db.query.member.findFirst({
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
}

export const getSession = createServerFn({ method: 'GET' })
  .middleware([sessionMiddleware])
  .handler(async ({ context }) => buildClientSession(getDb(), context.session))

export type ClientSession = Awaited<ReturnType<typeof getSession>>
