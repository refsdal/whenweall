import { createMiddleware } from '@tanstack/react-start'
import { getRequestHeaders } from '@tanstack/react-start/server'
import { getAuth } from './auth'
import { AppError } from '#/lib/errors'

export const sessionMiddleware = createMiddleware({ type: 'function' }).server(async ({ next }) => {
  const session = await getAuth().api.getSession({ headers: getRequestHeaders() })
  return next({ context: { session } })
})

export const requireSessionMiddleware = createMiddleware({ type: 'function' })
  .middleware([sessionMiddleware])
  .server(async ({ next, context }) => {
    if (!context.session) throw new AppError('UNAUTHORIZED')
    return next({ context: { session: context.session } })
  })
