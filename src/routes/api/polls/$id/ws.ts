import { createFileRoute } from '@tanstack/react-router'
import { pollRoom } from '#/server/notifications/do-client'
import { pollIdSchema } from '#/server/polls/schemas'

export const Route = createFileRoute('/api/polls/$id/ws')({
  server: {
    handlers: {
      GET: async ({ request, params }) => {
        if (request.headers.get('Upgrade') !== 'websocket') {
          return new Response('Expected websocket', { status: 426 })
        }
        if (!pollIdSchema.safeParse(params.id).success) {
          return new Response('Bad id', { status: 400 })
        }

        const url = new URL(request.url)
        url.searchParams.set('pollId', params.id)
        return pollRoom(params.id).fetch(new Request(url, request))
      },
    },
  },
})
