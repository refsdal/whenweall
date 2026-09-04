import { ApiError } from '#/api/client'
import { m } from '#/lib/i18n'

/**
 * True for the one error every captcha-gated auth route can raise before ever reaching Limen:
 * `internal/httpserver`'s `authCaptchaMiddleware`/`RequireCaptchaIfAnon` both answer 403
 * `{"error":{"code":"captcha_failed",...}}` when `X-Captcha-Token` is missing or Cloudflare
 * rejects it. Unlike every other case `authErrorMessage` matches below (Limen's own bare
 * `{"message":...}` shape, no `code` at all), this one DOES carry our own envelope, so it's
 * matched on `code` rather than English text — kept as its own export so a caller (the three
 * captcha-bearing auth forms) can branch on "was it specifically the captcha" without re-deriving
 * this check itself, e.g. to decide whether to reset a burned Turnstile token.
 */
export function isCaptchaFailedError(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'captcha_failed'
}

/**
 * Translates the handful of Limen error messages the auth UI needs a friendly message for (bad
 * credentials, a taken email, an expired reset/verify token) into a locale string. Limen's error
 * envelope carries no machine-readable `code` at all (see `#/api/auth.ts`'s own doc comment) —
 * only the exact English `message` its plugins raise (github.com/thecodearcher/limen's
 * credential-password `errors.go`: "invalid credential", "email already exists", "invalid or
 * expired token..."), so those are matched on that literal text rather than a code.
 *
 * A Turnstile token is single-use (`authCaptchaMiddleware` verifies AND redeems it before the
 * request ever reaches Limen), so a wrong password, a duplicate email, or any other Limen-side
 * rejection on the SAME request still burns it — the very next submit's stale token would
 * otherwise always fail with `captcha_failed` and fall through to the generic message below,
 * leaving the user stuck with no idea what to fix. isCaptchaFailedError is checked first (this
 * envelope DOES carry a code, unlike Limen's own) so that case gets its own actionable message
 * instead. Anything else still falls back to a generic "something went wrong" toast.
 */
export function authErrorMessage(error: unknown): string {
  if (isCaptchaFailedError(error)) {
    return m.auth_error_captcha_required()
  }
  const message = error instanceof ApiError ? error.message.toLowerCase() : ''
  if (message.includes('invalid credential') || message.includes('invalid password')) {
    return m.auth_error_invalid_credentials()
  }
  if (message.includes('already exists')) {
    return m.auth_error_email_taken()
  }
  if (message.includes('invalid or expired')) {
    return m.auth_error_invalid_token()
  }
  return m.auth_error_generic()
}
