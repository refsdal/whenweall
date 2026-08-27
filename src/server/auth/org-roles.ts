import { AppError } from '#/lib/errors'

/**
 * Pure org-role predicates, deliberately kept out of `./org.ts`.
 *
 * `org.ts` builds `requireOrgMiddleware`, so it pulls in `@tanstack/react-start` and, through
 * `requireSessionMiddleware`, Better-Auth and the Stripe SDK. The domain layer (`polls/service`,
 * `bookings/bookings`, `polls/claim-auth`, ...) only ever wants the role predicates below, and
 * those modules are what the Durable Objects import — so routing them through `org.ts` dragged
 * that whole graph into every DO bundle and into all 48 `*.workers.test.ts` isolates (~22s of
 * bundling per isolate). Import roles from here; import middleware from `./org.ts`.
 */
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
