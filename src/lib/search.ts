import * as z from 'zod'

/**
 * Matches a same-origin path only, once control characters are out of the way (see
 * `stripControlChars`). `startsWith('/')` alone isn't enough: `//evil.example` and
 * `/\evil.example` are both browser-legal protocol-relative URLs (the browser treats a leading
 * `//` or `/\` as "same scheme, different host"), and an encoded slash (`/%2fevil.example`,
 * `/%5cevil.example`) can decode to the same thing after a redirect. The negative lookahead
 * rejects a second `/`, a `\`, or an encoded slash/backslash immediately after the first `/`, and
 * the trailing character class rejects any raw control character left anywhere in the string.
 */
// eslint-disable-next-line no-control-regex -- deliberately matching raw control characters, see above
const SAFE_NEXT_PATTERN = /^\/(?!\/|\\|%2f|%5c)[^\x00-\x20\x7f]*$/i

/**
 * Encoded forms of the ASCII C0 control range (`%00`-`%1f`) and DEL (`%7f`). A browser's URL
 * parser strips raw TAB/CR/LF characters anywhere in a URL before doing anything else with it, so
 * `/\t/evil.example` becomes `//evil.example` — a protocol-relative bypass — the moment it's
 * handed to `location.href` or an `<a>` tag. The same shift happens if something downstream
 * percent-decodes an encoded control character first, so both forms are stripped up front here,
 * before the same-origin check ever runs.
 */
// eslint-disable-next-line no-control-regex -- deliberately matching raw control characters, see above
const ENCODED_CONTROL_CHAR_PATTERN = /%(?:0[0-9a-f]|1[0-9a-f]|7f)|[\x00-\x20\x7f]/gi

function stripControlChars(value: string): string {
  return value.replace(ENCODED_CONTROL_CHAR_PATTERN, '')
}

/** True when `value` is a safe same-origin `next` target, after normalising control characters. */
function isSafeNext(value: string): boolean {
  return SAFE_NEXT_PATTERN.test(stripControlChars(value))
}

/**
 * Shared `next` search param: where to send the user after an auth flow completes. Restricted to
 * same-origin paths so a crafted `?next=` can't redirect off-site.
 */
export const nextSearchSchema = z.object({
  next: z.string().refine(isSafeNext, 'Unsafe redirect target').optional(),
})

export type NextSearch = z.infer<typeof nextSearchSchema>

/**
 * Re-validates `next` at the point of use (defence in depth: `validateSearch` already rejects an
 * unsafe `next` before a route ever renders, but every redirect/navigate call site re-checks
 * rather than trusting that upstream validation ran).
 */
export function safeNext(next: string | undefined, fallback = '/'): string {
  return next && isSafeNext(next) ? next : fallback
}
