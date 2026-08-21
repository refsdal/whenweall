import * as z from 'zod'

/**
 * Matches a same-origin path only. `startsWith('/')` alone isn't enough: `//evil.example` and
 * `/\evil.example` are both browser-legal protocol-relative URLs (the browser treats a leading
 * `//` or `/\` as "same scheme, different host"), and an encoded slash (`/%2fevil.example`,
 * `/%5cevil.example`) can decode to the same thing after a redirect. The negative lookahead
 * rejects a second `/`, a `\`, or an encoded slash/backslash immediately after the first `/`.
 */
const SAFE_NEXT_PATTERN = /^\/(?!\/|\\|%2f|%5c)/i

/**
 * Shared `next` search param: where to send the user after an auth flow completes. Restricted to
 * same-origin paths so a crafted `?next=` can't redirect off-site.
 */
export const nextSearchSchema = z.object({
  next: z.string().regex(SAFE_NEXT_PATTERN).optional(),
})

export type NextSearch = z.infer<typeof nextSearchSchema>

/**
 * Re-validates `next` at the point of use (defence in depth: `validateSearch` already rejects an
 * unsafe `next` before a route ever renders, but every redirect/navigate call site re-checks
 * rather than trusting that upstream validation ran).
 */
export function safeNext(next: string | undefined, fallback = '/'): string {
  return next && SAFE_NEXT_PATTERN.test(next) ? next : fallback
}
