import { useReducer, useState } from 'react'
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useServerFn } from '@tanstack/react-start'
import { Save } from 'lucide-react'
import { toast } from 'sonner'
import { appConfig } from '#/app.config'
import { OptionsStep } from '#/components/creator/OptionsStep'
import { SettingsStep } from '#/components/creator/SettingsStep'
import { TypeStep } from '#/components/creator/TypeStep'
import { creatorReducer, draftFromPoll, draftToInput } from '#/components/creator/creator-state'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { getPoll, updatePoll } from '#/server/polls/polls.functions'
import { updatePollSchema, type OptionInput, type UpdatePollInput } from '#/server/polls/schemas'
import type { PollView } from '#/server/polls/viewmodel'

export const Route = createFileRoute('/p/$id/edit')({
  beforeLoad: ({ context, params }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: `/p/${params.id}/edit` } })
    }
  },
  loader: async ({ params }) => {
    const poll = await getPoll({ data: { pollId: params.id } })
    if (!poll.isOwner) throw redirect({ to: '/p/$id', params: { id: params.id } })
    return poll
  },
  head: ({ loaderData }) => ({
    meta: [{ title: `${m.editor_page_title()} — ${loaderData?.title ?? appConfig.name}` }],
  }),
  component: EditPollRoute,
})

/** How many of a poll's existing votes reference an option that the draft is about to drop. */
function countLostVotes(poll: PollView, options: OptionInput[]): number {
  const keptIds = new Set(options.map((option) => option.id).filter((id): id is string => !!id))
  const removedIds = new Set(
    poll.options.filter((option) => !keptIds.has(option.id)).map((option) => option.id),
  )
  if (removedIds.size === 0) return 0

  let count = 0
  for (const participant of poll.participants) {
    for (const optionId of Object.keys(participant.votes)) {
      if (removedIds.has(optionId)) count += 1
    }
  }
  return count
}

function EditPollRoute() {
  const poll = Route.useLoaderData()
  const navigate = Route.useNavigate()
  const updateFn = useServerFn(updatePoll)

  const [draft, dispatch] = useReducer(creatorReducer, poll, draftFromPoll)
  const [submitting, setSubmitting] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [pendingLostVotes, setPendingLostVotes] = useState(0)

  /**
   * Builds the `updatePoll` payload from the current draft. Unlike `draftToInput` (used by the
   * create wizard), a blank description/location is sent as an empty string rather than dropped:
   * on `updatePoll`, an *absent* field means "leave the current value alone", so dropping it here
   * would silently un-clear a field the organiser just emptied out.
   */
  function buildPayload(): UpdatePollInput | null {
    const input = draftToInput(draft)
    const payload: UpdatePollInput = {
      pollId: poll.id,
      title: input.title,
      description: draft.description.trim(),
      location: draft.location.trim(),
      timezone: input.timezone,
      deadlineAt: input.deadlineAt,
      requireParticipantEmail: input.requireParticipantEmail,
      allowComments: input.allowComments,
      allowIfNeedBe: input.allowIfNeedBe,
      options: input.options,
      ...(input.signupMaxClaims !== undefined ? { signupMaxClaims: input.signupMaxClaims } : {}),
    }
    return updatePollSchema.safeParse(payload).success ? payload : null
  }

  async function submit(payload: UpdatePollInput) {
    setSubmitting(true)
    try {
      await updateFn({ data: payload })
      toast.success(m.editor_updated())
      await navigate({ to: '/p/$id', params: { id: poll.id } })
    } catch (error) {
      toast.error(
        errorCode(error) === 'CAPACITY_BELOW_CLAIMS'
          ? m.editor_capacity_below_claims()
          : m.poll_error_generic(),
      )
    } finally {
      setSubmitting(false)
    }
  }

  async function handleSave() {
    const payload = buildPayload()
    if (!payload) {
      toast.error(m.creator_error_generic())
      return
    }

    const lostVotes = countLostVotes(poll, payload.options ?? [])
    if (lostVotes > 0) {
      setPendingLostVotes(lostVotes)
      setConfirmOpen(true)
      return
    }
    await submit(payload)
  }

  async function confirmSave() {
    setConfirmOpen(false)
    const payload = buildPayload()
    if (payload) await submit(payload)
  }

  return (
    <div
      data-testid="poll-editor"
      className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-5 py-10 sm:py-14"
    >
      <header className="flex flex-col gap-1">
        <h1 className="display text-3xl">{m.editor_page_title()}</h1>
        <p className="text-sm text-muted-foreground">{m.editor_page_subtitle()}</p>
      </header>

      <section className="surface flex flex-col gap-6 p-4 sm:p-6">
        <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {m.creator_step_basics()}
        </h2>
        <TypeStep draft={draft} dispatch={dispatch} onNext={() => {}} showTypeCards={false} />
      </section>

      <section className="surface flex flex-col gap-5 p-4 sm:p-6">
        <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {m.creator_step_options()}
        </h2>
        <OptionsStep draft={draft} dispatch={dispatch} />
      </section>

      <section className="surface flex flex-col gap-6 p-4 sm:p-6">
        <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {m.creator_step_settings()}
        </h2>
        <SettingsStep draft={draft} dispatch={dispatch} />
      </section>

      <div className="flex justify-end gap-3">
        <Button
          type="button"
          variant="ghost"
          onClick={() => void navigate({ to: '/p/$id', params: { id: poll.id } })}
        >
          {m.common_cancel()}
        </Button>
        <Button type="button" disabled={submitting} onClick={() => void handleSave()}>
          <Save aria-hidden="true" />
          {submitting ? m.editor_saving() : m.editor_save()}
        </Button>
      </div>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{m.editor_votes_warning_title()}</DialogTitle>
            <DialogDescription>
              {pendingLostVotes === 1
                ? m.editor_votes_warning_body_one()
                : m.editor_votes_warning_body_other({ count: pendingLostVotes })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setConfirmOpen(false)}>
              {m.common_cancel()}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={submitting}
              onClick={() => void confirmSave()}
            >
              {m.editor_votes_warning_confirm()}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
