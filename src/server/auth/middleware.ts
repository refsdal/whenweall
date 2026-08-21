import { createMiddleware } from '@tanstack/react-start'
// Imported from this deep subpath (rather than '@tanstack/react-start/server') so this
// module can be loaded inside the Workers vitest pool, which does not run the TanStack
// Start Vite plugin: the barrel re-export chain for '@tanstack/react-start/server' eagerly
// evaluates createStartHandler.js, which requires a "#tanstack-start-entry" import map entry
// only registered by that plugin. This is the same underlying implementation either way.
import { getRequestHeaders } from '@tanstack/start-server-core/request-response'
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
