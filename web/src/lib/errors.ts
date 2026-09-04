import { ApiError } from '#/api/client'
import { m } from '#/lib/i18n'

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

/**
 * A safe, translated detail line for the generic error boundary (`ErrorCard`), or `null` when
 * there is nothing safe to say beyond `error_body`'s generic sentence.
 *
 * Deliberately an allowlist keyed on `ApiError.code`, never `error.message`: an uncaught error
 * reaching the boundary carries whatever text its source produced, and that source is often the
 * server — a Postgres error names columns and tables, a failed upstream call can echo a whole
 * response body. Those strings were never written for a reader and never reviewed for what they
 * disclose, so none of them is rendered. The codes below are our own vocabulary, and each maps to
 * a sentence we wrote (see the `error_detail_*` / `error_rate_limited` messages).
 */
export function errorDetailMessage(error: unknown): string | null {
  switch (errorCode(error)) {
    case 'rate_limited':
      return m.error_rate_limited()
    case 'unauthenticated':
      return m.error_detail_unauthenticated()
    case 'forbidden':
      return m.error_detail_forbidden()
    default:
      return null
  }
}
