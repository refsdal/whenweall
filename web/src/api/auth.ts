import { api, ApiError } from '#/api/client'

/**
 * The Limen-backed auth client, replacing the old better-auth `authClient` (`#/server/auth/
 * client.ts`, deleted). Every route below is read off `internal/auth/routes.txt` (the "verified
 * route table" — see its own header comment) and the pinned Limen source
 * (github.com/thecodearcher/limen@c6a34aa6dcb4), NOT the old better-auth call sites the UI used to
 * make; the two libraries don't share a wire format.
 *
 * Two load-bearing differences from better-auth, both driven by Limen's own conventions:
 *
 * 1. Request bodies are snake_case (`credential`, `new_password`, `remember_me`, ...) — Limen's
 *    plugins validate against those exact keys (see credential-password's handlers.go). This is
 *    the ONE surface in this app that isn't camelCase; every other endpoint (polls/bookings/admin)
 *    is.
 * 2. Error responses are Limen's own `{"message": "..."}` shape, not our `{"error":{code,...}}`
 *    envelope (`buildLimenConfig` configures no `responseEnvelope`) — `client.ts`'s `parseErrorBody`
 *    already special-cases this, so `ApiError.code` here is only ever the generic status-based
 *    fallback (`unauthenticated`/`forbidden`/...), never a specific Limen error code. Call sites
 *    that need to distinguish "wrong password" from "already exists" read `ApiError.message`.
 */

export type AuthUser = {
  id: string
  name: string
  email: string
  emailVerified: boolean
  isStaff: boolean
}

/**
 * Limen's default user serialization would come back with NO usable id at all: `UserSchema.Serialize`
 * deletes the id column outright, and this backend configures no `WithPublicIDs` to replace it —
 * see `buildLimenConfig`'s own `sessionTransformer` (internal/auth/auth.go) for the fix, registered
 * via `limen.WithHTTPSessionTransformer`. Every signin/signup/me response is routed through that
 * transformer instead, which rebuilds the payload with `id` as a string of digits and adds
 * `isStaff` (backed by `staff_users`) — both guaranteed present on every response `toAuthUser` sees,
 * so this reads them directly rather than guessing between `id`/`ID` or probing a separate endpoint.
 */
function toAuthUser(raw: Record<string, unknown>): AuthUser {
  const id = typeof raw.id === 'string' ? raw.id : ''
  const firstName = typeof raw.first_name === 'string' ? raw.first_name : ''
  const lastName = typeof raw.last_name === 'string' ? raw.last_name : ''
  const email = typeof raw.email === 'string' ? raw.email : ''
  return {
    id,
    name: `${firstName} ${lastName}`.trim() || email,
    email,
    emailVerified: raw.email_verified_at != null,
    isStaff: raw.isStaff === true,
  }
}

type SessionResponse = { user: Record<string, unknown> }

export async function signInWithCredential(
  credential: string,
  password: string,
): Promise<AuthUser> {
  const { user } = await api<SessionResponse>('POST', '/api/v1/auth/signin/credential', {
    credential,
    password,
  })
  return toAuthUser(user)
}

export async function signUpWithCredential(email: string, password: string): Promise<AuthUser> {
  // No `name` field here on purpose: credential-password's SignUp handler only ever reads
  // `email`/`password`(/`username`) off the body (see this file's own doc comment) — there is no
  // first_name/last_name (or any other profile-update) route in routes.txt yet, so a name typed at
  // signup has nowhere to go. `AuthUser.name` falls back to the email until a later task adds one.
  const { user } = await api<SessionResponse>('POST', '/api/v1/auth/signup/credential', {
    email,
    password,
  })
  return toAuthUser(user)
}

export async function signOut(): Promise<void> {
  await api('POST', '/api/v1/auth/signout')
}

/** `GET /api/v1/auth/me` — `null` for no session (401), never throws for that case. */
export async function me(): Promise<AuthUser | null> {
  try {
    const { user } = await api<SessionResponse>('GET', '/api/v1/auth/me')
    return toAuthUser(user)
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return null
    throw err
  }
}

export async function requestPasswordReset(email: string): Promise<void> {
  await api('POST', '/api/v1/auth/passwords/request-reset', { email })
}

export async function resetPassword(token: string, newPassword: string): Promise<void> {
  await api('POST', '/api/v1/auth/passwords/reset', { token, new_password: newPassword })
}

export async function verifyEmail(token: string): Promise<void> {
  await api('POST', '/api/v1/auth/verify-email', { token })
}

/** Resends the signed-in caller's own verification email — protected (routes.txt), unlike the old
 * better-auth `sendVerificationEmail`, which worked for an anonymous caller too. A caller who just
 * failed to sign in because their email isn't verified may have no session to authorize this with;
 * that gap is a known limitation of this port (see the task report), not something invented here. */
export async function requestEmailVerification(): Promise<void> {
  await api('POST', '/api/v1/auth/email-verifications')
}

// ---- oauth --------------------------------------------------------------------------------------

type AuthorizeResponse = { url: string }

/** `GET /oauth/:provider/authorize` responds 200 `{"url": "..."}` rather than redirecting — the
 * caller navigates the browser there itself (routes.txt's own doc comment). */
export async function oauthAuthorizeUrl(provider: string, redirectUri?: string): Promise<string> {
  const { url } = await api<AuthorizeResponse>(
    'GET',
    `/api/v1/auth/oauth/${provider}/authorize`,
    undefined,
    { query: redirectUri ? { redirect_uri: redirectUri } : undefined },
  )
  return url
}

/** `GET /oauth/:provider/link` — the incremental-consent counterpart of `/authorize` for an
 * already-signed-in caller (protected). `scopes` is sent best-effort as a comma-joined query param
 * (no confirmed contract in routes.txt for extra scopes on this route — flagged in the report). */
export async function oauthLinkUrl(
  provider: string,
  opts?: { scopes?: string[]; redirectUri?: string },
): Promise<string> {
  const { url } = await api<AuthorizeResponse>(
    'GET',
    `/api/v1/auth/oauth/${provider}/link`,
    undefined,
    {
      query: {
        redirect_uri: opts?.redirectUri,
        scopes: opts?.scopes?.join(','),
      },
    },
  )
  return url
}

// ---- organizations --------------------------------------------------------------------------

export type ActiveOrg = { slug: string; name: string }

/** `GET /organizations/active` — `null` when the caller has no active organization (403
 * "no_active_org"-shaped failures from this route are swallowed the same way `me()` swallows a
 * 401: absence, not an error the UI needs to react to). */
export async function activeOrganization(): Promise<ActiveOrg | null> {
  try {
    const org = await api<Record<string, unknown>>('GET', '/api/v1/auth/organizations/active')
    const slug = typeof org.slug === 'string' ? org.slug : ''
    const name = typeof org.name === 'string' ? org.name : ''
    return { slug, name }
  } catch (err) {
    if (err instanceof ApiError && (err.status === 403 || err.status === 404)) return null
    throw err
  }
}

/**
 * `GET /organizations/me` — the caller's own role names in the active organization (e.g.
 * `["owner"]`; internal/auth/routes.txt's `organizations:member-get`). Used by settings.tsx's
 * `HandleSection` to decide whether to render the org-handle editor at all: `POST /api/v1/org/handle`
 * is gated server-side by `RequireOwnerRole` (internal/bookings/authz.go) — every non-owner org
 * member used to see the same editable field as the owner and only find out via a 403 toast after
 * submitting. `[]` when there is no active organization, the same non-error treatment
 * `activeOrganization()` already gives a 403/404 there.
 */
export async function myOrgRoles(): Promise<string[]> {
  try {
    const member = await api<Record<string, unknown>>('GET', '/api/v1/auth/organizations/me')
    return Array.isArray(member.roles)
      ? member.roles.filter((role): role is string => typeof role === 'string')
      : []
  } catch (err) {
    if (err instanceof ApiError && (err.status === 403 || err.status === 404)) return []
    throw err
  }
}

/** `POST /organizations/invitations/respond` — `invitationToken` is the `:token` path segment
 * from the emailed link (`/accept-invitation/$id` — the route param IS the token, per
 * `GET /organizations/invitations/token/:token`). */
export async function acceptInvitation(invitationToken: string): Promise<void> {
  await api('POST', '/api/v1/auth/organizations/invitations/respond', {
    token: invitationToken,
    response: 'accept',
  })
}
