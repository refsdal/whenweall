import { useRouteContext } from '@tanstack/react-router'
import * as authApi from '#/api/auth'
import { ApiError } from '#/api/client'
import { fetchAdminStats } from '#/api/admin'

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

/**
 * There is no "am I staff" field on Limen's own session (staff is our own concept — a
 * `staff_users` row, checked server-side by `auth.Service.RequireStaff` on every
 * `/api/v1/admin/*` route — internal/admin/handlers.go's own doc comment). Absent a dedicated
 * endpoint, this probes the cheapest staff-gated route and reads success/403 as the signal: a
 * real, server-verified answer rather than a guess, at the cost of one extra request per signed-in
 * session load.
 */
async function probeIsStaff(): Promise<boolean> {
  try {
    await fetchAdminStats()
    return true
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) return false
    throw err
  }
}

/** Fetches the current session: `null` when signed out. Called once from `__root.tsx`'s
 * `beforeLoad` (and again on every `router.invalidate()`). */
export async function fetchSession(): Promise<Session> {
  const user = await authApi.me()
  if (!user) return null
  const [org, isStaff] = await Promise.all([authApi.activeOrganization(), probeIsStaff()])
  return { user, org, isStaff }
}

/** Reads the session the root route already resolved. Throws outside a router tree — same
 * contract as every other `useRouteContext({from: '__root__'})` read in this app. */
export function useSession(): Session {
  return useRouteContext({ from: '__root__', select: (context) => context.session as Session })
}
