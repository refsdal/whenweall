import { useRouteContext } from '@tanstack/react-router'
import * as authApi from '#/api/auth'

/**
 * The Limen-backed replacement for better-auth's `useSession`. The session itself is fetched once
 * per navigation in `__root.tsx`'s `beforeLoad` (the same place `getSession()` lived before — see
 * that route's own doc comment) and threaded down through the router's own context, exactly like
 * `publicConfig`/`locale` already are; `useSession` is a thin, ergonomic read of that context for
 * any component that would rather not thread a `session` prop down from its route.
 *
 * This is deliberately NOT a separate React Context/provider with its own `fetch` + `useState`:
 * the router already re-runs `beforeLoad` (and so re-fetches the session) on `router.invalidate()`
 * — the same call every mutating action in this app already makes after a sign-in/sign-out/profile
 * change — so a second, independent piece of session state would only risk disagreeing with the
 * one route loaders see.
 */
export type SessionUser = authApi.AuthUser

export type SessionOrg = { slug: string; name: string } | null

export type Session = { user: SessionUser; org: SessionOrg; isStaff: boolean } | null

/** Fetches the current session: `null` when signed out. Called once from `__root.tsx`'s
 * `beforeLoad` (and again on every `router.invalidate()`).
 *
 * `isStaff` used to require its own request — a probe against a staff-gated admin route, reading
 * success/403 as the signal, since Limen's `/me` carried no such field on its own. `auth.Service`'s
 * `sessionTransformer` (internal/auth/auth.go) now puts `isStaff` directly on `/me`'s user object
 * (backed by `staff_users`, the same source that probe was standing in for), so this reads it off
 * `authApi.me()`'s result instead of making a second round trip. */
export async function fetchSession(): Promise<Session> {
  const user = await authApi.me()
  if (!user) return null
  const org = await authApi.activeOrganization()
  return { user, org, isStaff: user.isStaff }
}

/** Reads the session the root route already resolved. Throws outside a router tree — same
 * contract as every other `useRouteContext({from: '__root__'})` read in this app. */
export function useSession(): Session {
  return useRouteContext({ from: '__root__', select: (context) => context.session as Session })
}
