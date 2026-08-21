import { createFileRoute } from '@tanstack/react-router'
import { getDb } from '#/server/db/client'
import { buildPollIcs } from '#/server/polls/ics'

export const Route = createFileRoute('/p/$id/calendar.ics')({
  server: {
    handlers: {
      GET: async ({ params }) => {
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
