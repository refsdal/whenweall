import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useServerFn } from '@tanstack/react-start'
import { Check, MapPin, Minus, Share2, X } from 'lucide-react'
import { toast } from 'sonner'
import { AddYourselfRow } from '#/components/poll/AddYourselfRow'
import { AdminBar } from '#/components/poll/AdminBar'
import { Comments } from '#/components/poll/Comments'
import { DeadlineCountdown } from '#/components/poll/DeadlineCountdown'
import { FinalizedBanner } from '#/components/poll/FinalizedBanner'
import { optionPlainLabel } from '#/components/poll/OptionHeader'
import { PresencePill } from '#/components/poll/PresencePill'
import { ShareSheet } from '#/components/poll/ShareSheet'
import { storeTimeZone, TimezoneSwitch, useViewerTimeZone } from '#/components/poll/TimezoneSwitch'
import { VoteGrid } from '#/components/poll/VoteGrid'
import { canVote, type ViewerState } from '#/components/poll/viewer'
import { SlotBoard } from '#/components/signup/SlotBoard'
import { Button } from '#/components/ui/button'
import { celebrate } from '#/lib/confetti'
import { clearEditToken, useEditToken } from '#/lib/edit-tokens'
import { getLocale, m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import type { ClientSession } from '#/server/auth/session.functions'
import { removeParticipant } from '#/server/polls/participants.functions'
import type { ParticipantView, PollView } from '#/server/polls/viewmodel'

function Legend() {
  const items = [
    {
      className: 'bg-yes-soft text-yes-ink ring-1 ring-[var(--yes)]',
      Icon: Check,
      label: m.answer_yes(),
    },
    { className: 'bg-ifneedbe-soft text-ifneedbe-ink', Icon: Minus, label: m.answer_ifneedbe() },
    { className: 'bg-no-soft text-no-ink', Icon: X, label: m.answer_no() },
  ]

  return (
    <ul className="flex flex-wrap items-center gap-3">
      {items.map(({ className, Icon, label }) => (
        <li key={label} className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <span
            className={cn('flex size-4 items-center justify-center rounded-[0.3rem]', className)}
          >
            <Icon aria-hidden="true" className="size-2.5" strokeWidth={3} />
          </span>
          {label}
        </li>
      ))}
    </ul>
  )
}

/** "3 options · 5 people", or — on a sign-up sheet — "3 slots · 5 sign-ups". */
function metaOptions(poll: PollView): string {
  const count = poll.options.length
  if (poll.type === 'signup') {
    return count === 1 ? m.signup_meta_slots_one() : m.signup_meta_slots_other({ count })
  }
  return count === 1 ? m.poll_meta_options_one() : m.poll_meta_options_other({ count })
}

function metaPeople(poll: PollView): string {
  if (poll.type === 'signup') {
    // A sign-up count is claims, not participants — one person can hold several slots (up to
    // `signupMaxClaims`), and each of those is a sign-up worth counting.
    const count = Object.values(poll.claims).reduce((sum, claim) => sum + claim.count, 0)
    return count === 1 ? m.signup_meta_people_one() : m.signup_meta_people_other({ count })
  }
  const count = poll.participants.length
  return count === 1 ? m.poll_meta_people_one() : m.poll_meta_people_other({ count })
}

function StatusBadge({ poll }: { poll: PollView }) {
  if (poll.status === 'finalized') {
    return (
      <span className="rounded-full bg-yes-soft px-2.5 py-1 text-xs font-medium text-yes-ink">
        {m.poll_status_finalized()}
      </span>
    )
  }
  if (poll.status === 'closed') {
    return (
      <span className="rounded-full bg-secondary px-2.5 py-1 text-xs font-medium text-muted-foreground">
        {m.poll_status_closed()}
      </span>
    )
  }
  return null
}

/**
 * The poll itself: who can make it, when, and what the organiser decided.
 *
 * Everything that depends on the browser — the stored guest edit token, the visitor's timezone —
 * is resolved after mount, so the server-rendered page and the first client render agree.
 */
export function PollPage({
  poll,
  session,
  presence,
  shareUrl,
  autoOpenShare = false,
  onShareOpened,
  onChanged,
}: {
  poll: PollView
  session: ClientSession
  presence: number
  shareUrl: string
  autoOpenShare?: boolean
  onShareOpened?: () => void
  onChanged: () => void | Promise<void>
}) {
  const locale = getLocale()
  const removeFn = useServerFn(removeParticipant)

  const storedToken = useEditToken(poll.id)
  const storedZone = useViewerTimeZone(poll.timezone)
  const [chosenZone, setChosenZone] = useState<string | null>(null)
  const timeZone = chosenZone ?? storedZone
  const [editingId, setEditingId] = useState<string | null>(null)
  const [shareOpen, setShareOpen] = useState(autoOpenShare)

  // `?created` is a one-shot instruction: celebrate, open the share sheet, then take it out of
  // the URL so a reload or a shared link doesn't do either again.
  //
  // The confetti belongs here rather than in the creator: the wizard navigates away the moment
  // the poll exists, so a burst fired there would be thrown by a component already unmounting.
  const stripped = useRef(false)
  useEffect(() => {
    if (!autoOpenShare || stripped.current) return
    stripped.current = true
    celebrate('created')
    onShareOpened?.()
  }, [autoOpenShare, onShareOpened])

  const yourParticipant: ParticipantView | undefined = useMemo(() => {
    return poll.participants.find(
      (participant) =>
        (session !== null && participant.userId === session.user.id) ||
        participant.id === storedToken?.participantId,
    )
  }, [poll.participants, session, storedToken])

  const viewer: ViewerState = {
    userId: session?.user.id ?? null,
    participantId: yourParticipant?.id ?? null,
    editToken: storedToken?.token ?? null,
    isOwner: poll.isOwner,
    locale,
    timeZone,
  }

  const optionLabels = useMemo(
    () =>
      Object.fromEntries(
        poll.options.map((option) => [option.id, optionPlainLabel(option, locale, timeZone)]),
      ),
    [poll.options, locale, timeZone],
  )

  const isSignup = poll.type === 'signup'
  const open = canVote(poll)
  const editingParticipant = editingId
    ? poll.participants.find((participant) => participant.id === editingId)
    : undefined
  const showAddRow =
    !isSignup && open && (editingParticipant !== undefined || yourParticipant === undefined)
  // Slots are only worth re-reading in another timezone when they carry a time of day; a whole-day
  // slot and a text slot read the same everywhere.
  const showTimezone =
    poll.type === 'datetime' || (isSignup && poll.options.some((o) => o.kind === 'datetime'))

  const handleTimeZone = useCallback((zone: string) => {
    setChosenZone(zone)
    storeTimeZone(zone)
  }, [])

  const handleSaved = useCallback(async () => {
    setEditingId(null)
    await onChanged()
  }, [onChanged])

  const handleRemove = useCallback(
    async (participantId: string) => {
      try {
        await removeFn({
          data: {
            pollId: poll.id,
            participantId,
            editToken: storedToken?.token ?? undefined,
          },
        })
        if (participantId === storedToken?.participantId) clearEditToken(poll.id)
        if (participantId === editingId) setEditingId(null)
        toast.success(m.poll_removed())
        await onChanged()
      } catch {
        toast.error(m.poll_error_generic())
      }
    },
    [editingId, onChanged, poll.id, removeFn, storedToken],
  )

  const finalizedOption = poll.finalizedOptionId
    ? poll.options.find((option) => option.id === poll.finalizedOptionId)
    : undefined

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-5 py-8 sm:py-12">
      <header className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <StatusBadge poll={poll} />
          {poll.deadlineAt && poll.status === 'open' && (
            <DeadlineCountdown deadlineAt={poll.deadlineAt} />
          )}
          <PresencePill count={presence} />
        </div>

        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h1 className="display text-3xl text-balance sm:text-4xl">{poll.title}</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {m.poll_owner_by({ name: poll.owner.name })}
              {' · '}
              {metaOptions(poll)}
              {' · '}
              {metaPeople(poll)}
            </p>
          </div>

          <Button type="button" variant="outline" onClick={() => setShareOpen(true)}>
            <Share2 aria-hidden="true" />
            {m.poll_share()}
          </Button>
        </div>

        {poll.description && (
          <p className="max-w-2xl text-sm text-pretty whitespace-pre-wrap">{poll.description}</p>
        )}
        {poll.location && (
          <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <MapPin aria-hidden="true" className="size-3.5" />
            {poll.location}
          </p>
        )}
      </header>

      {finalizedOption && (
        <FinalizedBanner poll={poll} option={finalizedOption} locale={locale} timeZone={timeZone} />
      )}

      {poll.isOwner && (
        <AdminBar
          poll={poll}
          onChanged={onChanged}
          onShare={() => setShareOpen(true)}
          locale={locale}
          timeZone={timeZone}
          pushAvailable={session?.entitlements.push ?? false}
        />
      )}

      {!open && !finalizedOption && (
        <p className="rounded-xl bg-secondary px-4 py-3 text-sm text-muted-foreground">
          {m.poll_closed_notice()}
        </p>
      )}

      {isSignup ? (
        <SlotBoard
          poll={poll}
          session={session}
          locale={locale}
          timeZone={timeZone}
          onChanged={onChanged}
        />
      ) : (
        <section className="flex flex-col gap-3">
          <VoteGrid
            poll={poll}
            viewer={viewer}
            onEditParticipant={setEditingId}
            onRemoveParticipant={(participantId) => void handleRemove(participantId)}
            editingParticipantId={editingId}
            addRow={
              showAddRow ? (
                <AddYourselfRow
                  key={editingParticipant?.id ?? 'new'}
                  poll={poll}
                  session={session}
                  optionLabels={optionLabels}
                  existingParticipant={editingParticipant}
                  editToken={storedToken?.token ?? null}
                  onSaved={handleSaved}
                  onCancel={editingParticipant ? () => setEditingId(null) : undefined}
                />
              ) : null
            }
          />

          <div className="flex flex-wrap items-center justify-between gap-3">
            <Legend />
            {showTimezone && (
              <TimezoneSwitch
                value={timeZone}
                onChange={handleTimeZone}
                pollTimeZone={poll.timezone}
              />
            )}
          </div>
        </section>
      )}

      {isSignup && showTimezone && (
        <div className="flex justify-end">
          <TimezoneSwitch value={timeZone} onChange={handleTimeZone} pollTimeZone={poll.timezone} />
        </div>
      )}

      {poll.settings.allowComments && (
        <Comments
          poll={poll}
          session={session}
          viewer={viewer}
          canComment={poll.settings.allowComments}
          viewerName={yourParticipant?.name ?? session?.user.name ?? ''}
          onChanged={onChanged}
        />
      )}

      <ShareSheet url={shareUrl} open={shareOpen} onOpenChange={setShareOpen} />
    </div>
  )
}
