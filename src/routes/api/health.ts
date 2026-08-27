import { createFileRoute } from '@tanstack/react-router'
import { sql } from 'drizzle-orm'
import { getDb } from '#/server/db/client'

/**
 * Liveness probe for an external uptime monitor. External is the point: a Cloudflare-side alert
 * cannot fire when the problem is Cloudflare.
 *
 * The D1 round trip is deliberate — a worker that boots but cannot reach its database is down as
 * far as anyone using it is concerned, and a probe that only proves the isolate started would
 * report that as healthy. `select 1` is the cheapest statement that still crosses the binding.
 *
 * Unauthenticated (a monitor cannot hold a session) and therefore deliberately says nothing about
 * the deployment beyond up/down: no version, no commit, no binding names. `no-store` keeps it
 * from being served from a cache, which would defeat the whole point.
 */
export const Route = createFileRoute('/api/health')({
  server: {
    handlers: {
      GET: async () => {
        const headers = {
          'content-type': 'application/json',
          'cache-control': 'no-store',
          'x-robots-tag': 'noindex',
        }

        try {
          await getDb().run(sql`select 1`)
          return new Response(JSON.stringify({ ok: true }), { status: 200, headers })
        } catch (err) {
          console.error(JSON.stringify({ event: 'health.db_unreachable' }), err)
          return new Response(JSON.stringify({ ok: false }), { status: 503, headers })
        }
      },
    },
  },
})
