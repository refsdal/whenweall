import { env as workerEnv } from 'cloudflare:workers'

/** What Better-Auth's `rateLimit.customStorage` must implement. */
type RateLimitStorage = {
  consume: (
    key: string,
    rule: { window: number; max: number },
  ) => Promise<{ allowed: boolean; retryAfter: number | null }>
}

/**
 * Backs Better-Auth's rate limiting with `RateLimitRoom` instead of its default in-isolate `Map`.
 *
 * Better-Auth checks `customStorage` before any built-in option (see `getRateLimitStorage` in
 * node_modules/better-auth/dist/api/rate-limiter/index.mjs), so this replaces only the *storage*.
 * The policy — including its stricter built-in rules for sign-in, sign-up and password reset —
 * stays exactly as Better-Auth defines it.
 *
 * Keyed by the rate limit key Better-Auth builds, which already encodes the client IP and the
 * path bucket. Because a durable object is created near whatever first addressed it and that key
 * is IP-derived, the instance lands close to the requesting colo, so the added hop is a
 * same-region round trip rather than a transcontinental one.
 */
export function createRateLimitStorage(
  namespace: DurableObjectNamespace<
    import('#/do/RateLimitRoom').RateLimitRoom
  > = workerEnv.RATE_LIMIT_ROOM,
): RateLimitStorage {
  return {
    consume: async (key, rule) => {
      try {
        return await namespace.getByName(key).consume(key, rule.window, rule.max)
      } catch (err) {
        // Fail OPEN. An auth system that locks everyone out because a coordination service
        // hiccupped is worse than one that briefly stops rate limiting, and this path is reached
        // on every auth request — a durable object being momentarily unreachable must not become
        // a sitewide sign-in outage.
        console.error(JSON.stringify({ event: 'auth.rate_limit_unavailable' }), err)
        return { allowed: true, retryAfter: null }
      }
    },
  }
}
