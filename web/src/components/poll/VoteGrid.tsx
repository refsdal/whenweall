import { useState, type ReactNode } from 'react'
import { AnimatePresence } from 'motion/react'
import { OptionHeader, optionPlainLabel } from '#/components/poll/OptionHeader'
import { ParticipantRow } from '#/components/poll/ParticipantRow'
import { canEditParticipant, type ViewerState } from '#/components/poll/viewer'
import { m } from '#/lib/i18n'
import { useReducedMotion } from '#/lib/motion'
import { useCountUp } from '#/lib/use-count-up'
import { useNewlyArrived } from '#/lib/use-newly-arrived'
import { cn } from '#/lib/utils'
import type { PollView } from '#/api/types'

/**
 * A tally in the footer. Its own component because the count-up is a hook and the footer renders
 * one of these per option — and because the numbers move on their own here: a score that ticks up
 * as other people vote is the clearest sign the page is live.
 */
function ScoreCount({ value, className }: { value: number; className?: string }) {
  const reduceMotion = useReducedMotion()
  const display = useCountUp(value, !reduceMotion)
  return <span className={className}>{display}</span>
}

/**
 * The heart of the page: everyone's answers as one scannable table.
 *
 * Table semantics are the point — names are row headers, options are column headers, so a screen
 * reader announces "Iben, Tue 1 Sep 18:30: Yes" for any cell. On a narrow screen the table scrolls
 * sideways with the name column pinned, which is the only layout that survives eight options on a
 * phone.
 */
export function VoteGrid({
  poll,
  viewer,
  onEditParticipant,
  onRemoveParticipant,
  editingParticipantId,
  addRow,
}: {
  poll: PollView
  viewer: ViewerState
  onEditParticipant: (participantId: string) => void
  onRemoveParticipant: (participantId: string) => void
  /** While a row is being edited it is replaced by `addRow` in edit mode. */
  editingParticipantId?: string | null
  /** The "add yourself" row, rendered inside the table so the columns line up. */
  addRow?: ReactNode
}) {
  const optionLabels = Object.fromEntries(
    poll.options.map((option) => [
      option.id,
      optionPlainLabel(option, viewer.locale, viewer.timeZone),
    ]),
  )

  const columnCount = poll.options.length + 1
  const showEmptyState = poll.participants.length === 0

  // Your own row is never flashed: you know you just voted — you got confetti for it.
  const arrived = useNewlyArrived(poll.participants.map((participant) => participant.id))

  // Eight options on a wide grid make "which column am I in" a real question a few rows down.
  // One delegated handler on the table beats a pair on every cell, and `pointerover` bubbles
  // where `pointerenter` does not. Mice only: on a touch screen this would latch on tap and
  // stay lit with nothing to unlatch it.
  const [hoveredOption, setHoveredOption] = useState<string | null>(null)

  return (
    <div className="surface overflow-hidden">
      <div className="overflow-x-auto overscroll-x-contain">
        <table
          data-testid="vote-grid"
          className="w-full border-separate border-spacing-0 text-left"
          onPointerOver={(event) => {
            if (event.pointerType !== 'mouse') return
            const cell = (event.target as Element).closest('[data-option-id]')
            setHoveredOption(cell?.getAttribute('data-option-id') ?? null)
          }}
          onPointerLeave={() => setHoveredOption(null)}
        >
          <caption className="sr-only">{poll.title}</caption>

          <thead>
            <tr>
              <th
                scope="col"
                className="sticky left-0 z-20 min-w-36 bg-card px-3 pt-2 pb-1 align-bottom text-[0.6875rem] font-medium tracking-wide text-muted-foreground uppercase sm:min-w-48"
              >
                <span className="block pb-3">{m.poll_grid_name_header()}</span>
              </th>
              {poll.options.map((option) => (
                <OptionHeader
                  key={option.id}
                  option={option}
                  locale={viewer.locale}
                  timeZone={viewer.timeZone}
                  best={option.id === poll.bestOptionId && poll.finalizedOptionId === null}
                  finalized={option.id === poll.finalizedOptionId}
                  hovered={option.id === hoveredOption}
                />
              ))}
            </tr>
          </thead>

          <tbody>
            <AnimatePresence initial={false}>
              {poll.participants
                .filter((participant) => participant.id !== editingParticipantId)
                .map((participant) => {
                  const isYou =
                    participant.id === viewer.participantId ||
                    (viewer.userId !== null && participant.userId === viewer.userId)

                  return (
                    <ParticipantRow
                      key={participant.id}
                      participant={participant}
                      options={poll.options}
                      optionLabels={optionLabels}
                      isYou={isYou}
                      justArrived={!isYou && arrived.has(participant.id)}
                      hoveredOptionId={hoveredOption}
                      canEdit={canEditParticipant(poll, viewer, participant.id)}
                      onEdit={onEditParticipant}
                      onRemove={onRemoveParticipant}
                      bestOptionId={poll.finalizedOptionId === null ? poll.bestOptionId : null}
                      finalizedOptionId={poll.finalizedOptionId}
                      allowIfNeedBe={poll.settings.allowIfNeedBe}
                    />
                  )
                })}
            </AnimatePresence>

            {showEmptyState && (
              <tr>
                <td
                  colSpan={columnCount}
                  className={cn('border-t border-border px-4', addRow ? 'py-5' : 'py-10')}
                >
                  <p className="text-center text-sm font-medium">{m.poll_empty_title()}</p>
                  <p className="mt-1 text-center text-sm text-muted-foreground">
                    {m.poll_empty_body()}
                  </p>
                </td>
              </tr>
            )}

            {addRow}
          </tbody>

          <tfoot>
            <tr>
              <th
                scope="row"
                className="sticky left-0 z-10 border-t border-border bg-card px-3 py-2 text-left text-[0.6875rem] font-medium tracking-wide text-muted-foreground uppercase"
              >
                {m.poll_grid_scores_header()}
              </th>
              {poll.options.map((option) => {
                const score = poll.scores[option.id] ?? { yes: 0, ifneedbe: 0, no: 0, score: 0 }
                const isBest = option.id === poll.bestOptionId && poll.finalizedOptionId === null
                const isFinalized = option.id === poll.finalizedOptionId
                return (
                  <td
                    key={option.id}
                    data-testid={`score-${option.id}`}
                    data-option-id={option.id}
                    data-yes={String(score.yes)}
                    data-ifneedbe={String(score.ifneedbe)}
                    data-best={isBest ? 'true' : undefined}
                    data-finalized={isFinalized ? 'true' : undefined}
                    className={cn(
                      'border-t border-border px-1 py-2 text-center transition-colors',
                      isBest && 'bg-accent-soft/35',
                      isFinalized && 'bg-yes-soft/35',
                      option.id === hoveredOption && !isBest && !isFinalized && 'bg-secondary/60',
                    )}
                  >
                    <span className="sr-only">
                      {m.poll_score_yes({ count: score.yes })}
                      {score.ifneedbe > 0
                        ? `, ${m.poll_score_ifneedbe({ count: score.ifneedbe })}`
                        : ''}
                    </span>
                    <span aria-hidden="true" className="flex items-baseline justify-center gap-1">
                      <ScoreCount
                        value={score.yes}
                        className={cn(
                          'text-sm font-semibold tabular-nums',
                          score.yes > 0 ? 'text-yes-ink' : 'text-muted-foreground',
                        )}
                      />
                      {score.ifneedbe > 0 && (
                        <span className="text-[0.6875rem] font-medium tabular-nums text-ifneedbe-ink">
                          +
                          <ScoreCount value={score.ifneedbe} />
                        </span>
                      )}
                    </span>
                  </td>
                )
              })}
            </tr>
          </tfoot>
        </table>
      </div>
    </div>
  )
}
