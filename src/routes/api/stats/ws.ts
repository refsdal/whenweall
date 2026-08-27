import { createFileRoute } from '@tanstack/react-router'
import { statsRoom } from '#/server/stats/stats-client'

export const Route = createFileRoute('/api/stats/ws')({
  server: {
    handlers: {
      GET: ({ request }) => {
        if (request.headers.get('Upgrade') !== 'websocket') {
          return new Response('Expected websocket', { status: 426 })
        }
        return statsRoom().fetch(request)
      },
    },
  },
})
