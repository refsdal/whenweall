import { AppError } from '#/lib/errors'

/**
 * Pure platform-role predicates, deliberately kept out of `./staff.ts` — the same split, and the
 * same reason, as `org-roles.ts` vs `org.ts`: `staff.ts` builds middleware and so pulls in
 * `@tanstack/react-start` and Better-Auth, which must not be dragged into every Durable Object
 * bundle and workers test isolate just to ask whether someone is staff.
 */

/**
 * A user's role across the whole platform, stored on `user.role`.
 *
 * This is NOT `OrgRole`. `OrgRole` ('owner' | 'admin' | 'member') lives on `member` and describes
 * someone's standing inside one organisation. The two are unrelated, which is exactly why the
 * privileged value here is 'staff' and never 'admin' — a single `role === 'admin'` check written
 * against the wrong one would hand every organisation admin the keys to the platform.
 */
export type PlatformRole = 'user' | 'staff'

const STAFF: PlatformRole = 'staff'

/**
 * Whether this session may use the admin console.
 *
 * `impersonatedBy` is checked as well as the role, and that is the point rather than a detail.
 * While impersonating, the session belongs to the person being impersonated for every purpose
 * except ending the impersonation. Treating it as staff would let an admin wield admin powers
 * *as another user* — a privilege escalation whose audit trail would name the wrong person.
 */
export function isStaff(input: { role?: string | null; impersonatedBy?: string | null }): boolean {
  if (input.impersonatedBy) return false
  return input.role === STAFF
}

/** `isStaff`, as a guard. Throws `FORBIDDEN` so callers map it like any other authorization failure. */
export function requireStaff(input: {
  role?: string | null
  impersonatedBy?: string | null
}): void {
  if (!isStaff(input)) throw new AppError('FORBIDDEN')
}
