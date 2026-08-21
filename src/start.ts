import { createMiddleware, createStart } from '@tanstack/react-start'
import { setResponseHeader } from '@tanstack/start-server-core/request-response'
import { env } from 'cloudflare:workers'

const CONTENT_SECURITY_POLICY = [
  "default-src 'self'",
  "script-src 'self' 'unsafe-inline' https://challenges.cloudflare.com",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: https:",
  "font-src 'self' data:",
  "connect-src 'self' wss: https://challenges.cloudflare.com",
  'frame-src https://challenges.cloudflare.com',
  "frame-ancestors 'none'",
  "base-uri 'self'",
  "form-action 'self'",
].join('; ')

/**
 * Applies baseline security headers to every server response (SSR documents, server routes, and
 * server functions alike). Header-setting is wrapped in try/catch because a handful of responses
 * — notably the 101 websocket upgrade from `/api/polls/$id/ws` — don't support having headers
 * mutated after the fact, and that must never break the underlying response.
 */
export const securityHeaders = createMiddleware().server(async ({ next }) => {
  const result = await next()

  try {
    setResponseHeader('Content-Security-Policy', CONTENT_SECURITY_POLICY)
    setResponseHeader('X-Content-Type-Options', 'nosniff')
    setResponseHeader('Referrer-Policy', 'strict-origin-when-cross-origin')
    setResponseHeader('Permissions-Policy', 'camera=(), microphone=(), geolocation=()')

    const appEnv: string = env.APP_ENV
    if (appEnv === 'production') {
      setResponseHeader('Strict-Transport-Security', 'max-age=31536000; includeSubDomains')
    }
  } catch (err) {
    console.error('[start] failed to set security headers', err)
  }

  return result
})

export const startInstance = createStart(() => ({
  requestMiddleware: [securityHeaders],
}))
