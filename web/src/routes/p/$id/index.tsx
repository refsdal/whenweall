import { useCallback } from 'react'
import { createFileRoute, useRouter } from '@tanstack/react-router'
import * as z from 'zod'
import { appConfig } from '#/app.config'
import { NotFoundCard } from '#/components/layout/NotFoundCard'
import { PollPage } from '#/components/poll/PollPage'
import { m } from '#/lib/i18n'
import { useEditToken } from '#/lib/edit-tokens'
import { type PollEvent, useLivePoll } from '#/lib/use-live-poll'
import { getPoll } from '#/api/polls'

/** `?created` is set by the creator right after a poll is made; it opens the share sheet once. */
const searchSchema = z.object({
  created: z.union([z.boolean(), z.literal('1'), z.literal('true')]).optional(),
})

export const Route = createFileRoute('/p/$id/')({
  validateSearch: searchSchema,
  loader: ({ params }) => getPoll(params.id),
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
  const editToken = useEditToken(poll.id)

  // Every change to the poll — a vote, a comment, a finalize, the deadline alarm closing it —
  // arrives here as one event; re-running the loader is the simplest correct response.
  //
  // `useLivePoll` already forwards a synthetic `poll.changed` for the snapshot every connect and
  // reconnect gets (PROTOCOL.md: a snapshot always reflects state as of that connect, which is
  // exactly the "a vote cast before this socket opened is missed for good otherwise" catch-up the
  // old `connected`-triggered effect existed for) and for `resync` (the room's own rule: treat it
  // like a fresh connect), so this one handler covers all three cases.
  const onEvent = useCallback(
    (event: PollEvent) => {
      if (event.type === 'poll.changed') void router.invalidate()
    },
    [router],
  )
  const { presence } = useLivePoll(poll.id, onEvent, editToken?.token)

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
    <NotFoundCard
      title={m.poll_not_found_title()}
      body={m.poll_not_found_body()}
      ctaLabel={m.poll_not_found_cta()}
    />
  )
}
