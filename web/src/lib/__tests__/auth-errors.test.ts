import { describe, expect, it } from 'vitest'
import { ApiError } from '#/api/client'
import { authErrorMessage, isCaptchaFailedError } from '#/lib/auth-errors'
import { m } from '#/lib/i18n'

describe('authErrorMessage', () => {
  it('maps invalid credentials', () => {
    expect(authErrorMessage(new ApiError('unauthenticated', 'invalid credential', 401))).toBe(
      m.auth_error_invalid_credentials(),
    )
  })

  it('maps a taken email', () => {
    expect(authErrorMessage(new ApiError('conflict', 'email already exists', 409))).toBe(
      m.auth_error_email_taken(),
    )
  })

  it('maps an invalid/expired token', () => {
    expect(authErrorMessage(new ApiError('invalid', 'invalid or expired token', 400))).toBe(
      m.auth_error_invalid_token(),
    )
  })

  // internal/httpserver's authCaptchaMiddleware/RequireCaptchaIfAnon both answer 403
  // {"error":{"code":"captcha_failed","message":"captcha verification failed"}} — a Turnstile
  // token is single-use, so a wrong password or any other Limen-side rejection on the SAME
  // request still burns it. Matching on `code` (rather than the English message, like every other
  // branch here) is what lets the three auth forms tell "the captcha itself failed" apart from
  // "credentials/uniqueness/token failed", so they can reset the widget only when it's actually
  // the captcha at fault.
  it('maps captcha_failed by code, distinct from a generic failure', () => {
    expect(authErrorMessage(new ApiError('captcha_failed', 'captcha verification failed', 403))).toBe(
      m.auth_error_captcha_required(),
    )
  })

  it('falls back to a generic message for anything unrecognized', () => {
    expect(authErrorMessage(new ApiError('unknown', 'boom', 500))).toBe(m.auth_error_generic())
    expect(authErrorMessage(new Error('not an ApiError'))).toBe(m.auth_error_generic())
  })
})

describe('isCaptchaFailedError', () => {
  it('is true only for an ApiError coded captcha_failed', () => {
    expect(isCaptchaFailedError(new ApiError('captcha_failed', 'captcha verification failed', 403))).toBe(
      true,
    )
    expect(isCaptchaFailedError(new ApiError('unauthenticated', 'invalid credential', 401))).toBe(false)
    expect(isCaptchaFailedError(new Error('boom'))).toBe(false)
    expect(isCaptchaFailedError(null)).toBe(false)
  })
})
