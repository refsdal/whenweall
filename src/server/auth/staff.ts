import { createMiddleware } from '@tanstack/react-start'
import { requireSessionMiddleware } from './middleware'
import { requireStaff } from './platform-roles'

/**
 * The gate every admin server function takes.
 *
 * Middleware lives here rather than beside the predicates in `platform-roles.ts` for the reason
 * spelled out in `org-roles.ts`: this module reaches `@tanstack/react-start` and, through
 * `requireSessionMiddleware`, Better-Auth and the Stripe SDK.
 *
 * A route's `beforeLoad` guard is for navigation only and must never be the sole gate — routes
 * are a UI concern, and a server function is reachable without ever rendering one.
 */
export const requireStaffMiddleware = createMiddleware({ type: 'function' })
  .middleware([requireSessionMiddleware])
  .server(async ({ next, context }) => {
    const user = context.session.user as { id: string; email: string; role?: string | null }
    const session = context.session.session as { impersonatedBy?: string | null }

    requireStaff({ role: user.role, impersonatedBy: session.impersonatedBy })

    // Carried so the audit writer never has to re-read the actor, and so `actorEmail` is captured
    // from the live session rather than trusted from a request body.
    return next({ context: { staff: { userId: user.id, email: user.email } } })
  })
