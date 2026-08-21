import { m } from '#/lib/i18n'

/**
 * Translates the handful of Better-Auth error codes the auth UI needs a friendly message for
 * (bad credentials, a taken email, an expired reset/verify token). Anything else falls back to a
 * generic "something went wrong" toast rather than surfacing Better-Auth's English-only message.
 */
export function authErrorMessage(error: { code?: string } | null | undefined): string {
  switch (error?.code) {
    case 'INVALID_EMAIL_OR_PASSWORD':
      return m.auth_error_invalid_credentials()
    case 'USER_ALREADY_EXISTS':
    case 'USER_ALREADY_EXISTS_USE_ANOTHER_EMAIL':
      return m.auth_error_email_taken()
    case 'INVALID_TOKEN':
    case 'EXPIRED_TOKEN':
      return m.auth_error_invalid_token()
    default:
      return m.auth_error_generic()
  }
}
