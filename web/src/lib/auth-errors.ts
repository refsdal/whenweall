import { ApiError } from '#/api/client'
import { m } from '#/lib/i18n'

/**
 * Translates the handful of Limen error messages the auth UI needs a friendly message for (bad
 * credentials, a taken email, an expired reset/verify token) into a locale string. Limen's error
 * envelope carries no machine-readable `code` at all (see `#/api/auth.ts`'s own doc comment) —
 * only the exact English `message` its plugins raise (github.com/thecodearcher/limen's
 * credential-password `errors.go`: "invalid credential", "email already exists", "invalid or
 * expired token..."), so this matches on that literal text rather than a code. Anything else falls
 * back to a generic "something went wrong" toast.
 */
export function authErrorMessage(error: unknown): string {
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
