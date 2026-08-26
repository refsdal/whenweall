import { createServerFn } from '@tanstack/react-start'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import { sessionMiddleware } from './middleware'
import type { Session } from './auth'
import { resolveActiveOrg, type OrgRole } from './org'
import {
  FREE_ENTITLEMENTS,
  getEntitlements,
  type Entitlements,
} from '#/server/billing/entitlements'

export type ClientSessionPayload = {
  user: {
    id: string
    name: string
    email: string
    image: string | null
    locale: string | null
  }
  org: { id: string; slug: string; name: string; role: OrgRole } | null
  entitlements: Entitlements
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

  const org = await resolveActiveOrg(db, s)
  const entitlements = org ? await getEntitlements(db, org.id) : FREE_ENTITLEMENTS

  return {
    user: {
      id: s.user.id,
      name: s.user.name,
      email: s.user.email,
      image: s.user.image ?? null,
      locale: (s.user as { locale?: string }).locale ?? null,
    },
    org: org ? { id: org.id, slug: org.slug, name: org.name, role: org.role } : null,
    entitlements,
  }
}

export const getSession = createServerFn({ method: 'GET' })
  .middleware([sessionMiddleware])
  .handler(async ({ context }) => buildClientSession(getDb(), context.session))

export type ClientSession = Awaited<ReturnType<typeof getSession>>
