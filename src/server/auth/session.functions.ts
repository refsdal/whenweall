import { createServerFn } from '@tanstack/react-start'
import { sessionMiddleware } from './middleware'

export const getSession = createServerFn({ method: 'GET' })
  .middleware([sessionMiddleware])
  .handler(({ context }) => {
    const s = context.session
    return s
      ? {
          user: {
            id: s.user.id,
            name: s.user.name,
            email: s.user.email,
            image: s.user.image ?? null,
            locale: (s.user as { locale?: string }).locale ?? null,
            // Better-Auth `additionalFields`, so it rides along on the session user; the booking
            // UI needs it to render `/book/<handle>/<slug>` links.
            handle: (s.user as { handle?: string }).handle ?? null,
          },
        }
      : null
  })

export type ClientSession = Awaited<ReturnType<typeof getSession>>
