/**
 * The typed fetch core every `web/src/api/*.ts` module is built on. Talks to the Go backend's
 * REST surface (internal/polls, internal/bookings, internal/admin `handlers.go`) directly — there
 * is no server-function/RPC layer anymore, just `fetch` against `/api/v1` (proxied to the Go
 * process in dev — see
 * `vite.config.ts`'s `server.proxy`).
 *
 * Every non-2xx response is expected to carry the standard envelope
 * `{"error":{"code","message","fields"?}}` (see `internal/httpserver/respond.go`'s `Err`) and is
 * unwrapped into an `ApiError`. A response body that ISN'T that shape (a network failure, an
 * upstream 502 from a proxy, ...) still becomes an `ApiError`, with a best-effort code/message so
 * callers never have to special-case "the JSON didn't parse".
 */

/** One field-path -> message entry from a 422 "invalid" response's `fields` map. */
export type ApiErrorFields = Record<string, string>

/**
 * The envelope's `error.code` is the Go backend's own snake_case vocabulary (`not_found`,
 * `poll_closed`, `slot_taken`, ...) — see the endpoint tables in internal/polls, internal/bookings,
 * internal/admin, and internal/auth's routes.txt. Call sites compare against these directly
 * (documented in the task report as a deliberate simplification over the old TS `SCREAMING_CASE`
 * `AppError` codes): one vocabulary, no mapping table to keep in sync.
 */
export class ApiError extends Error {
  code: string
  status: number
  fields?: ApiErrorFields

  constructor(code: string, message: string, status: number, fields?: ApiErrorFields) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
    this.fields = fields
  }
}

type ApiOpts = {
  /** Sent as `X-Guest-Token` — a poll participant's or booking's guest credential (see
   * `web/src/lib/guest-tokens.ts`). */
  guestToken?: string
  /** Sent as `X-Captcha-Token` — a Cloudflare Turnstile response, for a public mutating endpoint's
   * anonymous callers (`internal/httpserver.RequireCaptchaIfAnon`). */
  captchaToken?: string
  /** Overrides the request's query string. */
  query?: Record<string, string | number | boolean | undefined>
  /** AbortSignal passthrough, for callers that want to cancel an in-flight request. */
  signal?: AbortSignal
}

function buildUrl(path: string, query?: ApiOpts['query']): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined) params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `${path}?${qs}` : path
}

/** The handful of statuses the UI branches on, mapped to the same codes the Go backend's own
 * envelope would carry — used whenever a response doesn't actually have that envelope (a bare
 * 401/403 from a middleware short-circuit, a proxy's own HTML error page, a network failure). */
function codeForStatus(status: number): { code: string; message: string } {
  if (status === 401) return { code: 'unauthenticated', message: 'authentication required' }
  if (status === 403) return { code: 'forbidden', message: 'forbidden' }
  if (status === 404) return { code: 'not_found', message: 'not found' }
  if (status === 429) return { code: 'rate_limited', message: 'rate limited' }
  return { code: 'unknown', message: 'request failed' }
}

async function parseErrorBody(response: Response): Promise<ApiError> {
  const fallback = codeForStatus(response.status)

  let parsed: unknown
  try {
    parsed = await response.json()
  } catch {
    return new ApiError(fallback.code, response.statusText || fallback.message, response.status)
  }
  const errorBody =
    parsed !== null && typeof parsed === 'object' && 'error' in parsed
      ? (parsed as { error?: unknown }).error
      : undefined
  if (errorBody !== null && typeof errorBody === 'object') {
    const { code, message, fields } = errorBody as {
      code?: unknown
      message?: unknown
      fields?: unknown
    }
    return new ApiError(
      typeof code === 'string' ? code : fallback.code,
      typeof message === 'string' ? message : response.statusText || fallback.message,
      response.status,
      fields !== null && typeof fields === 'object' ? (fields as ApiErrorFields) : undefined,
    )
  }
  // Limen's own auth surface (internal/auth/routes.txt) uses its own bare `{"message": "..."}`
  // shape, not our `{"error":{code,message,fields}}` envelope (buildLimenConfig configures no
  // responseEnvelope) — no `code` to read, but the message is worth keeping over a generic
  // statusText (e.g. "invalid credentials" vs. "Unauthorized").
  if (parsed !== null && typeof parsed === 'object' && 'message' in parsed) {
    const { message } = parsed as { message?: unknown }
    if (typeof message === 'string' && message.length > 0) {
      return new ApiError(fallback.code, message, response.status)
    }
  }
  // Not our envelope shape at all (e.g. a proxy's own HTML error page) — the status-based
  // fallback above is the best signal left.
  return new ApiError(fallback.code, response.statusText || fallback.message, response.status)
}

/**
 * Calls `path` on the Go backend and returns the decoded JSON body (`T`), or throws `ApiError`.
 * `credentials: 'same-origin'` carries the Limen session cookie; every non-GET/HEAD body is sent
 * as `Content-Type: application/json` (Limen's own CSRF check requires exactly that on a mutating
 * request — see `internal/auth/routes.txt`).
 */
export async function api<T>(method: string, path: string, body?: unknown, opts?: ApiOpts): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (opts?.guestToken) headers['X-Guest-Token'] = opts.guestToken
  if (opts?.captchaToken) headers['X-Captcha-Token'] = opts.captchaToken

  const response = await fetch(buildUrl(path, opts?.query), {
    method,
    headers,
    credentials: 'same-origin',
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal: opts?.signal,
  })

  if (!response.ok) throw await parseErrorBody(response)

  if (response.status === 204 || response.headers.get('Content-Length') === '0') {
    return undefined as T
  }
  const text = await response.text()
  if (text.length === 0) return undefined as T
  return JSON.parse(text) as T
}
