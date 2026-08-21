import { createFileRoute } from '@tanstack/react-router'
import { getDb } from '#/server/db/client'
import { buildPollIcs } from '#/server/polls/ics'
import { pollIdSchema } from '#/server/polls/schemas'

export const Route = createFileRoute('/p/$id/calendar.ics')({
  server: {
    handlers: {
      GET: async ({ params }) => {
        if (!pollIdSchema.safeParse(params.id).success) {
          return new Response('Bad id', { status: 400 })
        }

        const ics = await buildPollIcs(getDb(), params.id)
        if (!ics) return new Response('Not found', { status: 404 })

        return new Response(ics, {
          headers: {
            'Content-Type': 'text/calendar; charset=utf-8',
            'Content-Disposition': `attachment; filename="samla-${params.id}.ics"`,
          },
        })
      },
    },
  },
})
