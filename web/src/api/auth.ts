import { api, ApiError } from '#/api/client'
import { appConfig, type AppLocale } from '#/app.config'
import { getLocale, isAppLocale } from '#/lib/i18n'

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

/** The app's locale union, re-exported under the name the rest of the auth surface uses. */
export type Locale = AppLocale

export type AuthUser = {
  id: string
  name: string
  email: string
  emailVerified: boolean
  locale: Locale
  /** Whether a password credential exists — the delete-account dialog asks for the current
   * password only when there is one (an OAuth-only account has nothing to re-enter). */
  hasPassword: boolean
  isStaff: boolean
}

/**
 * Limen's default user serialization would come back with NO usable id at all: `UserSchema.Serialize`
 * deletes the id column outright, and this backend configures no `WithPublicIDs` to replace it —
 * see `buildLimenConfig`'s own `sessionTransformer` (internal/auth/auth.go) for the fix, registered
 * via `limen.WithHTTPSessionTransformer`. Every signin/signup/me response is routed through that
 * transformer instead, which rebuilds the payload with `id` as a string of digits and adds
 * `isStaff` (staff_users), `name` (display name), `locale` (user_preferences), `emailVerified`
 * and `hasPassword` — all guaranteed present on every response `toAuthUser` sees. The fallbacks
 * below only matter for a payload from an older server during a rolling deploy.
 */
function toAuthUser(raw: Record<string, unknown>): AuthUser {
  const id = typeof raw.id === 'string' ? raw.id : ''
  const email = typeof raw.email === 'string' ? raw.email : ''
  const firstName = typeof raw.first_name === 'string' ? raw.first_name : ''
  const lastName = typeof raw.last_name === 'string' ? raw.last_name : ''
  const composedName = `${firstName} ${lastName}`.trim() || email
  const rawLocale = typeof raw.locale === 'string' ? raw.locale : ''
  return {
    id,
    name: typeof raw.name === 'string' && raw.name.length > 0 ? raw.name : composedName,
    email,
    emailVerified:
      typeof raw.emailVerified === 'boolean' ? raw.emailVerified : raw.email_verified_at != null,
    locale: isAppLocale(rawLocale) ? rawLocale : appConfig.defaultLocale,
    hasPassword: raw.hasPassword === true,
    isStaff: raw.isStaff === true,
  }
}

type SessionResponse = { user: Record<string, unknown> }

/** Turns the optional captcha token every auth form passes into the `api()` option — `null`/
 * `undefined` means "captcha is off or not solved", and then NO header is sent (the Go middleware
 * only demands one when Turnstile is configured). */
function captchaOpts(captchaToken?: string | null): { captchaToken?: string } {
  return captchaToken ? { captchaToken } : {}
}

export async function signInWithCredential(
  credential: string,
  password: string,
  captchaToken?: string | null,
): Promise<AuthUser> {
  const { user } = await api<SessionResponse>(
    'POST',
    '/api/v1/auth/signin/credential',
    { credential, password },
    captchaOpts(captchaToken),
  )
  return toAuthUser(user)
}

/**
 * Signup sends `name` and `locale` alongside Limen's own `email`/`password`: Limen ignores both,
 * but `internal/auth`'s After hook on the signup route (signup_hook.go) reads them off the same
 * body and stores them. Signup does NOT mint a session (auto-sign-in is off — the account is
 * unusable until the mailed verification link is clicked), so the returned user is informational.
 */
export async function signUpWithCredential(
  email: string,
  password: string,
  name: string,
  captchaToken?: string | null,
): Promise<AuthUser> {
  const { user } = await api<SessionResponse>(
    'POST',
    '/api/v1/auth/signup/credential',
    { email, password, name, locale: getLocale() },
    captchaOpts(captchaToken),
  )
  return toAuthUser(user)
}

export async function signOut(): Promise<void> {
  await api('POST', '/api/v1/auth/signout')
}

/**
 * `GET /api/v1/auth/me` — `null` for no session (401) AND for a locked account (403 from
 * internal/auth's AuthMountGuard: "account is locked"). Both mean "you are not signed in as far as
 * the UI is concerned"; the login page tells a locked user what happened (it signs in, then sees
 * `me()` come back null). Never throws for either case.
 */
export async function me(): Promise<AuthUser | null> {
  try {
    const { user } = await api<SessionResponse>('GET', '/api/v1/auth/me')
    return toAuthUser(user)
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) return null
    throw err
  }
}

export async function requestPasswordReset(email: string, captchaToken?: string | null): Promise<void> {
  await api('POST', '/api/v1/auth/passwords/request-reset', { email }, captchaOpts(captchaToken))
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

// ---- own account (internal/httpserver/account.go) -----------------------------------------

/** `PATCH /api/v1/me` — works before email verification too (an unverified user may still pick
 * the language of the verification mail we resend them). */
export async function updateProfile(patch: { name?: string; locale?: string }): Promise<void> {
  await api('PATCH', '/api/v1/me', patch)
}

/** `DELETE /api/v1/me` — a credential account must send its current password (400
 * `password_required` / 403 `invalid_password` otherwise); an OAuth-only account sends nothing. */
export async function deleteOwnAccount(password?: string): Promise<void> {
  await api('DELETE', '/api/v1/me', password ? { password } : {})
}

export type OrgSummary = { id: string; name: string; slug: string; active: boolean }

/** `GET /api/v1/me/organizations` — our own route, not Limen's `GET /organizations/`: Limen
 * serializes organizations WITHOUT an id (see routes.txt), and switching needs one. */
export function listOrganizations(): Promise<OrgSummary[]> {
  return api<OrgSummary[]>('GET', '/api/v1/me/organizations')
}

/** `POST /api/v1/me/active-organization` — membership is verified server-side (403 otherwise). */
export async function switchOrganization(orgId: string): Promise<void> {
  await api('POST', '/api/v1/me/active-organization', { orgId })
}

/**
 * Accepts an invitation: reads it by token first (`GET /organizations/invitations/token/:token`
 * embeds the organization's `slug`; the respond route does not), then `POST
 * /organizations/invitations/respond`. Returns the joined organization's slug so the caller can
 * find it in `listOrganizations()` and `switchOrganization()` to it — Limen's respond route does
 * not change the session's active organization. `invitationToken` is the `/accept-invitation/$id`
 * route param (the param IS the token).
 */
export async function acceptInvitation(invitationToken: string): Promise<{ orgSlug: string | null }> {
  const invitation = await api<{ organization?: { slug?: unknown } }>(
    'GET',
    `/api/v1/auth/organizations/invitations/token/${encodeURIComponent(invitationToken)}`,
  )
  const orgSlug = typeof invitation.organization?.slug === 'string' ? invitation.organization.slug : null
  await api('POST', '/api/v1/auth/organizations/invitations/respond', {
    token: invitationToken,
    response: 'accept',
  })
  return { orgSlug }
}
