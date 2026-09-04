import { ApiError } from '#/api/client'

/**
 * The Go backend's own error-code vocabulary (snake_case, from internal/httpserver's envelope —
 * see internal/polls/handlers.go's mapServiceError and internal/bookings/handlers.go's own, plus
 * internal/httpserver's shared unauthenticated/forbidden/rate_limited/invalid). The old TS
 * `AppError`'s SCREAMING_CASE codes (`ERROR_CODES` below, deleted) are gone: call sites now compare
 * `ApiError.code` against these directly rather than through a mapping table — one vocabulary, kept
 * in sync with the backend by construction (there's nothing to translate).
 */
export const ERROR_CODES = [
  'unauthenticated',
  'forbidden',
  'not_found',
  'invalid',
  'conflict',
  'capacity_full',
  'slot_taken',
  'poll_closed',
  'poll_finalized',
  'limit_reached',
  'claim_limit_reached',
  'capacity_below_claims',
  'email_required',
  'page_paused',
  'booking_past',
  'slug_taken',
  'handle_taken',
  'google_not_connected',
  'invalid_token',
  'rate_limited',
  'captcha_failed',
] as const

export type ErrorCode = (typeof ERROR_CODES)[number]

/** `error.code` for an `ApiError`, `null` for anything else — the replacement for the old
 * `errorCode()` (which unwrapped `AppError`). Call sites `switch` on the returned string directly;
 * it isn't narrowed to `ErrorCode` because an unrecognized/future backend code must still compare
 * false against every `case`, not be silently coerced into one of these. */
export function errorCode(error: unknown): string | null {
  return error instanceof ApiError ? error.code : null
}
