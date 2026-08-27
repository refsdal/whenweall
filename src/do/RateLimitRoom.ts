import { DurableObject } from 'cloudflare:workers'

/** A single decision, matching what Better-Auth's `customStorage.consume` must return. */
export type RateLimitDecision = { allowed: boolean; retryAfter: number | null }

/** One counter's state. `resetAt` is wall-clock ms, so a window is closed by time, not by ticks. */
type Window = { count: number; resetAt: number }

/**
 * How many expired entries may accumulate before a sweep runs. A DO addressed by IP sees a
 * handful of keys, but one addressed by a shared/NAT'd address can see many, and nothing else
 * ever removes them.
 */
const SWEEP_THRESHOLD = 256

/**
 * Fixed-window rate limit counters, shared across isolates and colos.
 *
 * This exists because Better-Auth's default rate limit storage is a `Map` inside the isolate.
 * Cloudflare runs many isolates across many colos and evicts them aggressively, so each request
 * can meet a fresh, empty counter — traffic that is merely geographically spread walks straight
 * through it. A durable object is the cheapest way to get one counter that every request agrees
 * on, and unlike the D1-backed option it costs no database traffic on the auth hot path.
 *
 * State is held **in memory only**, never in `ctx.storage`, for two reasons: a rate limit counter
 * is worth less than the write it would cost to persist, and losing one on eviction fails open
 * for at most a single window. It also keeps this durable object free of the IP-derived data that
 * would otherwise make it subject to the same jurisdiction question as the rest of our storage.
 */
export class RateLimitRoom extends DurableObject<Env> {
  #windows = new Map<string, Window>()

  /**
   * Records one hit against `key` and says whether it is allowed.
   *
   * `window` is in seconds (Better-Auth's unit); `retryAfter` is returned in seconds for the same
   * reason. A fixed window rather than a sliding one: it is what Better-Auth's own storage
   * implements, so swapping the backend does not quietly change the policy.
   */
  consume(key: string, window: number, max: number): RateLimitDecision {
    const now = Date.now()
    const entry = this.#windows.get(key)

    if (!entry || now >= entry.resetAt) {
      if (this.#windows.size >= SWEEP_THRESHOLD) this.#sweep(now)
      this.#windows.set(key, { count: 1, resetAt: now + window * 1000 })
      return { allowed: true, retryAfter: null }
    }

    if (entry.count < max) {
      entry.count += 1
      return { allowed: true, retryAfter: null }
    }

    return { allowed: false, retryAfter: Math.max(1, Math.ceil((entry.resetAt - now) / 1000)) }
  }

  /** Drops windows that have already closed. Called opportunistically, never on a timer. */
  #sweep(now: number): void {
    for (const [key, entry] of this.#windows) {
      if (now >= entry.resetAt) this.#windows.delete(key)
    }
  }

  /** Test seam: how many windows are currently tracked. */
  size(): number {
    return this.#windows.size
  }
}
