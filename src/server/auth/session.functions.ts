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
          },
        }
      : null
  })

export type ClientSession = Awaited<ReturnType<typeof getSession>>
