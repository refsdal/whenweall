import { useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { ChevronDown } from 'lucide-react'
import { BestBadge } from '#/components/poll/BestBadge'
import { optionPlainLabel } from '#/components/poll/OptionHeader'
import { ANSWER_STYLES, answerLabel, Mark } from '#/components/poll/VoteCell'
import type { ViewerState } from '#/components/poll/viewer'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { formatOptionLabel } from '#/lib/time'
import { nextAnswer, type Answer } from '#/lib/scoring'
import { cn } from '#/lib/utils'
import type { ParticipantView, PollOptionView, PollView } from '#/api/types'

/** Initials for the little avatar chip, as in the grid's name column. */
function initial(name: string): string {
  return name.trim().slice(0, 1).toUpperCase() || '?'
}

/**
 * The tally to draw for one option: the saved scores, with the viewer's own answer swapped for
 * whatever they have unsaved.
 *
 * Swapped rather than added — someone editing their existing row already appears in `poll.scores`,
 * and counting the draft on top of that would show them voting twice.
 */
function tallyFor({
  option,
  poll,
  ownParticipant,
  draft,
  live,
}: {
  option: PollOptionView
  poll: PollView
  ownParticipant: ParticipantView | undefined
  draft: Answer | null
  /** False on a closed poll, where there is no draft to fold in. */
  live: boolean
}): { yes: number; ifneedbe: number } {
  const base = poll.scores[option.id] ?? { yes: 0, ifneedbe: 0, no: 0, score: 0 }
  let yes = base.yes
  let ifneedbe = base.ifneedbe
  if (!live) return { yes, ifneedbe }

  const stored = ownParticipant?.votes[option.id] ?? null
  if (stored === 'yes') yes -= 1
  else if (stored === 'ifneedbe') ifneedbe -= 1
  if (draft === 'yes') yes += 1
  else if (draft === 'ifneedbe') ifneedbe += 1

  return { yes, ifneedbe }
}

/** The viewer's own answer: a thumb-sized target, or a read-only mark once the poll is shut. */
function YourAnswer({
  answer,
  optionLabel,
  onCycle,
}: {
  answer: Answer | null
  optionLabel: string
  /** Absent on a read-only poll. */
  onCycle?: () => void
}) {
  const label = m.poll_list_answer_label({ option: optionLabel, answer: answerLabel(answer) })
  const shared = cn(
    'flex items-center justify-center transition-[background-color,box-shadow] duration-200',
    'size-13 shrink-0 rounded-xl',
    ANSWER_STYLES[answer ?? 'none'].className,
  )

  if (!onCycle) {
    return (
      <span
        role="img"
        data-testid="your-answer"
        data-answer={answer ?? 'none'}
        aria-label={label}
        className={shared}
      >
        <Mark answer={answer} size="lg" />
      </span>
    )
  }

  return (
    <button
      type="button"
      data-testid="your-answer"
      data-answer={answer ?? 'none'}
      aria-label={label}
      onClick={onCycle}
      className={cn(shared, 'focus-ring cursor-pointer')}
    >
      <Mark answer={answer} size="lg" />
    </button>
  )
}

/**
 * The poll as a list of dates, one per row — the phone layout.
 *
 * The grid is the right shape for a desktop and the wrong shape for a phone: a frozen name column
 * takes 43% of a 390px screen, which leaves room for two dates however many the poll has. Turning
 * it on its side removes the sideways scroll entirely. What it gives up is comparing two people
 * against each other at a glance, so the grid stays on anything wider.
 */
export function VoteList({
  poll,
  viewer,
  answers,
  onAnswer,
  canAnswer,
}: {
  poll: PollView
  viewer: ViewerState
  /** The viewer's unsaved answers, shared with the grid's add-yourself row. */
  answers: Record<string, Answer>
  onAnswer: (optionId: string, answer: Answer | null) => void
  /** Whether the viewer may change their answer right now (open poll, and their row to edit). */
  canAnswer: boolean
}) {
  const reduceMotion = useReducedMotion()
  const [openId, setOpenId] = useState<string | null>(null)

  const ownParticipant = poll.participants.find(
    (participant) =>
      participant.id === viewer.participantId ||
      (viewer.userId !== null && participant.userId === viewer.userId),
  )

  // A visitor who has not answered yet is about to become one more person on the sheet, so the
  // denominator counts them from the moment they can answer.
  const total = poll.participants.length + (canAnswer && ownParticipant === undefined ? 1 : 0)

  return (
    <div className="flex flex-col gap-3">
      {poll.participants.length === 0 && (
        <div className="rounded-xl border border-dashed border-border px-4 py-5 text-center">
          <p className="text-sm font-medium">{m.poll_empty_title()}</p>
          <p className="mt-1 text-sm text-muted-foreground">{m.poll_empty_body()}</p>
        </div>
      )}

      <ul data-testid="vote-list" className="flex flex-col gap-2.5">
        {poll.options.map((option) => {
          const label = formatOptionLabel(option, {
            locale: viewer.locale,
            timeZone: viewer.timeZone,
          })
          const plainLabel = optionPlainLabel(option, viewer.locale, viewer.timeZone)
          const finalized = option.id === poll.finalizedOptionId
          const best = option.id === poll.bestOptionId && poll.finalizedOptionId === null
          const stored = ownParticipant?.votes[option.id] ?? null
          const draft = answers[option.id] ?? null
          const mine = canAnswer ? draft : stored
          const { yes, ifneedbe } = tallyFor({
            option,
            poll,
            ownParticipant,
            draft,
            live: canAnswer,
          })
          const open = openId === option.id
          const share = (count: number) => (total === 0 ? 0 : (count / total) * 100)

          return (
            <li
              key={option.id}
              data-testid={`vote-row-${option.id}`}
              data-best={best ? 'true' : undefined}
              data-finalized={finalized ? 'true' : undefined}
              data-yes={String(yes)}
              data-ifneedbe={String(ifneedbe)}
              data-total={String(total)}
              className={cn(
                'overflow-hidden rounded-xl border bg-card transition-colors',
                best && 'border-[var(--primary)] bg-accent-soft/40',
                finalized && 'border-[var(--yes)] bg-yes-soft/40',
                !best && !finalized && 'border-border',
              )}
            >
              <div className="flex items-center gap-3 py-3 pr-3 pl-4">
                <button
                  type="button"
                  aria-expanded={open}
                  aria-label={`${plainLabel} — ${m.poll_list_people_toggle()}`}
                  onClick={() => setOpenId(open ? null : option.id)}
                  className="focus-ring flex min-w-0 flex-1 cursor-pointer flex-col items-start gap-1.5 rounded-md py-1"
                >
                  {(best || finalized) && (
                    <BestBadge
                      variant={finalized ? 'picked' : 'best'}
                      layoutGroup="poll-crown-list"
                    />
                  )}

                  <span className="text-base font-semibold tracking-[-0.01em]">
                    {label.primary}
                  </span>
                  {label.secondary && (
                    <span className="text-sm tabular-nums text-muted-foreground">
                      {label.secondary}
                      {label.tertiary ? ` ${label.tertiary}` : ''}
                    </span>
                  )}

                  <span className="flex w-full items-center gap-2 pt-0.5">
                    <span className="flex h-1.5 flex-1 overflow-hidden rounded-full bg-secondary">
                      <span
                        className="bg-[var(--yes)]"
                        style={{ width: `${share(yes)}%` }}
                        aria-hidden="true"
                      />
                      <span
                        className="bg-[var(--ifneedbe)]"
                        style={{ width: `${share(ifneedbe)}%` }}
                        aria-hidden="true"
                      />
                    </span>
                    <span className="text-xs font-medium tabular-nums whitespace-nowrap text-muted-foreground">
                      {m.poll_list_tally({ yes, total })}
                    </span>
                    <ChevronDown
                      aria-hidden="true"
                      className={cn(
                        'size-4 shrink-0 text-muted-foreground transition-transform duration-200',
                        open && 'rotate-180',
                      )}
                    />
                  </span>
                </button>

                {(canAnswer || mine !== null) && (
                  <span className="flex shrink-0 flex-col items-center gap-1">
                    <span
                      aria-hidden="true"
                      className="text-[0.625rem] font-semibold tracking-wide text-muted-foreground uppercase"
                    >
                      {m.poll_list_your_answer()}
                    </span>
                    <YourAnswer
                      answer={mine}
                      optionLabel={plainLabel}
                      onCycle={
                        canAnswer
                          ? () => onAnswer(option.id, nextAnswer(mine, poll.settings.allowIfNeedBe))
                          : undefined
                      }
                    />
                  </span>
                )}
              </div>

              <AnimatePresence initial={false}>
                {open && (
                  <motion.div
                    initial={reduceMotion ? false : { height: 0, opacity: 0 }}
                    animate={{ height: 'auto', opacity: 1 }}
                    exit={reduceMotion ? { opacity: 0 } : { height: 0, opacity: 0 }}
                    transition={spring}
                    className="overflow-hidden"
                  >
                    <ul className="flex flex-col border-t border-border px-4 py-1">
                      {poll.participants.length === 0 && (
                        <li className="py-2.5 text-sm text-muted-foreground">
                          {m.poll_empty_title()}
                        </li>
                      )}
                      {poll.participants.map((participant) => {
                        const answer = participant.votes[option.id] ?? null
                        const isYou = participant.id === ownParticipant?.id
                        return (
                          <li
                            key={participant.id}
                            className="flex items-center gap-2.5 border-b border-border/60 py-2.5 last:border-b-0"
                          >
                            <span
                              aria-hidden="true"
                              className={cn(
                                'inline-flex size-7 shrink-0 items-center justify-center rounded-full text-[0.6875rem] font-semibold',
                                isYou
                                  ? 'bg-primary-strong text-primary-foreground'
                                  : 'bg-secondary text-secondary-foreground',
                              )}
                            >
                              {initial(participant.name)}
                            </span>
                            <span className="min-w-0 flex-1 truncate text-sm">
                              {participant.name}
                            </span>
                            <span
                              data-answer={answer ?? 'none'}
                              className={cn(
                                'inline-flex shrink-0 items-center rounded-full px-2.5 py-1 text-xs font-semibold',
                                answer === 'yes' && 'bg-yes-soft text-yes-ink',
                                answer === 'ifneedbe' && 'bg-ifneedbe-soft text-ifneedbe-ink',
                                answer === 'no' && 'bg-no-soft text-no-ink',
                                answer === null && 'bg-secondary text-muted-foreground',
                              )}
                            >
                              {answerLabel(answer)}
                            </span>
                          </li>
                        )
                      })}
                    </ul>
                  </motion.div>
                )}
              </AnimatePresence>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
