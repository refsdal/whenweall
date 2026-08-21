import { useCallback } from 'react'
import { createFileRoute, Link, useRouter } from '@tanstack/react-router'
import { SearchX } from 'lucide-react'
import * as z from 'zod'
import { appConfig } from '#/app.config'
import { PollPage } from '#/components/poll/PollPage'
import { Button } from '#/components/ui/button'
import type { PollEvent } from '#/do/protocol'
import { m } from '#/lib/i18n'
import { useLivePoll } from '#/lib/use-live-poll'
import { getPoll } from '#/server/polls/polls.functions'

/** `?created` is set by the creator right after a poll is made; it opens the share sheet once. */
const searchSchema = z.object({
  created: z.union([z.boolean(), z.literal('1'), z.literal('true')]).optional(),
})

export const Route = createFileRoute('/p/$id/')({
  validateSearch: searchSchema,
  loader: ({ params }) => getPoll({ data: { pollId: params.id } }),
  head: ({ loaderData }) => ({
    meta: [
      { title: `${loaderData?.title ?? m.poll_not_found_title()} — ${appConfig.name}` },
      { property: 'og:title', content: loaderData?.title ?? appConfig.name },
      {
        property: 'og:description',
        content: m.og_poll_description({ count: loaderData?.options.length ?? 0 }),
      },
      { property: 'og:type', content: 'website' },
    ],
  }),
  component: PollRoute,
  notFoundComponent: PollNotFound,
})

function PollRoute() {
  const poll = Route.useLoaderData()
  const { created } = Route.useSearch()
  const navigate = Route.useNavigate()
  const { session } = Route.useRouteContext()
  const router = useRouter()

  // Every change to the poll — a vote, a comment, a finalize, the deadline alarm closing it —
  // arrives here as one event; re-running the loader is the simplest correct response.
  const onEvent = useCallback(
    (event: PollEvent) => {
      if (event.type === 'poll.changed') void router.invalidate()
    },
    [router],
  )
  const { presence } = useLivePoll(poll.id, onEvent)

  const onChanged = useCallback(() => router.invalidate(), [router])

  const origin = typeof window === 'undefined' ? '' : window.location.origin
  const shareUrl = `${origin}/p/${poll.id}`

  return (
    <PollPage
      poll={poll}
      session={session}
      presence={presence}
      shareUrl={shareUrl}
      autoOpenShare={created !== undefined && created !== false}
      onShareOpened={() => void navigate({ search: {}, replace: true })}
      onChanged={onChanged}
    />
  )
}

function PollNotFound() {
  return (
    <div className="mx-auto flex w-full max-w-lg flex-col items-center gap-4 px-5 py-24 text-center">
      <span className="inline-flex size-12 items-center justify-center rounded-full bg-secondary text-muted-foreground">
        <SearchX aria-hidden="true" className="size-5" />
      </span>
      <h1 className="display text-2xl">{m.poll_not_found_title()}</h1>
      <p className="text-sm text-muted-foreground">{m.poll_not_found_body()}</p>
      <Button asChild variant="outline">
        <Link to="/">{m.poll_not_found_cta()}</Link>
      </Button>
    </div>
  )
}
