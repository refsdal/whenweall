/**
 * The rate limiter itself. The middleware that wraps it lives in `rate-limit.middleware.ts`,
 * apart from this module so that a client component importing a rate-limited server function
 * never drags `cloudflare:workers` into the browser bundle.
 */
// Imported from this deep subpath (rather than '@tanstack/react-start/server') so this
// module can be loaded inside the Workers vitest pool, which does not run the TanStack
// Start Vite plugin: the barrel re-export chain for '@tanstack/react-start/server' eagerly
// evaluates createStartHandler.js, which requires a "#tanstack-start-entry" import map entry
// only registered by that plugin. This is the same underlying implementation either way.
import { getRequestHeader } from '@tanstack/start-server-core/request-response'
import { env } from 'cloudflare:workers'
import { AppError } from '#/lib/errors'

export type RateLimitAction = 'create' | 'vote' | 'comment' | 'auth' | 'book'

export function clientIp(): string {
  try {
    return getRequestHeader('cf-connecting-ip') ?? 'unknown'
  } catch {
    return 'unknown'
  }
}

export async function enforceRateLimit(
  action: RateLimitAction,
  key?: string,
  limiter: RateLimit | null = env.RATE_LIMITER ?? null,
): Promise<void> {
  if (!limiter) return

  const rateLimitKey = `${action}:${key ?? clientIp()}`
  const { success } = await limiter.limit({ key: rateLimitKey })
  if (!success) throw new AppError('RATE_LIMITED')
}
