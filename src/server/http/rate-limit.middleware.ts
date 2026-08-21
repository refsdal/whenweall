import { createMiddleware } from '@tanstack/react-start'
import { enforceRateLimit, type RateLimitAction } from './rate-limit'

/*
 * The rate-limit middlewares live apart from the limiter itself, and each is a top-level
 * `createMiddleware(...).server(...)` chain rather than something a factory builds.
 *
 * Both details matter for the client bundle. The TanStack Start plugin strips `.server()` bodies
 * from a server function's middleware chain and then drops the imports only those bodies used —
 * so `./rate-limit`, and with it `cloudflare:workers`, never reaches the browser. It can only do
 * that for chains it can see at the top level: built inside a factory, the body survives into
 * every client component that imports a server function using it (the poll creator imports
 * `createPoll`), and the client build fails to resolve `cloudflare:workers`.
 */
const createLimit = createMiddleware({ type: 'function' }).server(async ({ next }) => {
  await enforceRateLimit('create')
  return next()
})

const voteLimit = createMiddleware({ type: 'function' }).server(async ({ next }) => {
  await enforceRateLimit('vote')
  return next()
})

const commentLimit = createMiddleware({ type: 'function' }).server(async ({ next }) => {
  await enforceRateLimit('comment')
  return next()
})

// Reserved for future use: Better-Auth's own handler owns `/api/auth/*`, so no server function
// currently takes this one. Kept so auth endpoints can be limited without re-deriving the shape.
const authLimit = createMiddleware({ type: 'function' }).server(async ({ next }) => {
  await enforceRateLimit('auth')
  return next()
})

const MIDDLEWARE = {
  create: createLimit,
  vote: voteLimit,
  comment: commentLimit,
  auth: authLimit,
} as const

/** The rate-limit middleware for one action, e.g. `rateLimitMiddleware('vote')`. */
export function rateLimitMiddleware<A extends RateLimitAction>(action: A): (typeof MIDDLEWARE)[A] {
  return MIDDLEWARE[action]
}
