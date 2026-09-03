import { redirect } from '@tanstack/react-router'
import type { Session } from '#/lib/use-session'

/**
 * The `beforeLoad` guard for every route that needs a usable account: signed out → /login (with
 * `next` back here); signed in but unverified → /verify-email, where the pending card offers a
 * resend. Mirrors the server: internal/auth's RequireSession answers 403 email_unverified for an
 * unverified session, so a route that skipped this would only get as far as its loader's error.
 */
export function requireVerifiedSession(context: { session: Session }, next: string): void {
  if (!context.session) {
    throw redirect({ to: '/login', search: { next } })
  }
  if (!context.session.user.emailVerified) {
    throw redirect({ to: '/verify-email', search: {} })
  }
}
