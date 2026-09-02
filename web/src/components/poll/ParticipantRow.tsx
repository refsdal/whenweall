import { useState } from 'react'
import { motion } from 'motion/react'
import { Pencil, Trash2 } from 'lucide-react'
import { VoteCell } from '#/components/poll/VoteCell'
import { Button } from '#/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '#/components/ui/popover'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'
import type { ParticipantView, PollOptionView } from '#/server/polls/viewmodel'

/** Initials for the little avatar chip in the name column. */
function initial(name: string): string {
  return name.trim().slice(0, 1).toUpperCase() || '?'
}

export function ParticipantRow({
  participant,
  options,
  optionLabels,
  isYou,
  justArrived = false,
  canEdit,
  onEdit,
  onRemove,
  bestOptionId,
  finalizedOptionId,
  hoveredOptionId = null,
  allowIfNeedBe,
}: {
  participant: ParticipantView
  options: PollOptionView[]
  optionLabels: Record<string, string>
  isYou: boolean
  /** Someone else added this row while the visitor was watching; it flashes once. */
  justArrived?: boolean
  canEdit: boolean
  onEdit: (participantId: string) => void
  onRemove: (participantId: string) => void
  bestOptionId: string | null
  finalizedOptionId: string | null
  /** The column the pointer is in, so the whole of it lights up rather than the one cell. */
  hoveredOptionId?: string | null
  allowIfNeedBe: boolean
}) {
  const reduceMotion = useReducedMotion()
  const [confirming, setConfirming] = useState(false)

  return (
    <motion.tr
      layout={reduceMotion ? false : 'position'}
      initial={reduceMotion ? false : { opacity: 0, y: 6 }}
      animate={{ opacity: 1, y: 0 }}
      exit={reduceMotion ? { opacity: 0 } : { opacity: 0, y: -6 }}
      transition={spring}
      data-testid={`participant-row-${participant.id}`}
      data-arrived={justArrived ? 'true' : undefined}
      className="group/row"
    >
      <th
        scope="row"
        className={cn(
          // The sticky column slides over the answer cells, so its background has to stay fully
          // opaque — "your row" is marked with an ember edge rather than a translucent tint.
          'sticky left-0 z-10 border-t border-border bg-card px-3 py-1.5 text-left font-normal',
          isYou && 'border-l-2 border-l-[var(--primary)]',
        )}
      >
        <div className="flex items-center gap-2">
          <span
            aria-hidden="true"
            className={cn(
              'inline-flex size-6 shrink-0 items-center justify-center rounded-full text-[0.625rem] font-semibold',
              isYou
                ? 'bg-primary-strong text-primary-foreground'
                : 'bg-secondary text-secondary-foreground',
            )}
          >
            {initial(participant.name)}
          </span>
          <span className="min-w-0 flex-1 truncate text-sm" title={participant.name}>
            {participant.name}
          </span>
          {isYou && (
            <span className="shrink-0 rounded-full bg-accent-soft px-1.5 py-0.5 text-[0.625rem] font-semibold text-accent-foreground">
              {m.poll_you_badge()}
            </span>
          )}

          {canEdit && (
            <span className="flex shrink-0 items-center opacity-70 transition-opacity focus-within:opacity-100 group-hover/row:opacity-100 sm:opacity-0">
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={m.poll_edit_row({ name: participant.name })}
                onClick={() => onEdit(participant.id)}
              >
                <Pencil aria-hidden="true" />
              </Button>
              <Popover open={confirming} onOpenChange={setConfirming}>
                <PopoverTrigger asChild>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    aria-label={m.poll_remove_row({ name: participant.name })}
                  >
                    <Trash2 aria-hidden="true" />
                  </Button>
                </PopoverTrigger>
                <PopoverContent align="start" className="w-64">
                  <PopoverHeader>
                    <PopoverTitle>
                      {m.poll_remove_confirm_title({ name: participant.name })}
                    </PopoverTitle>
                    <PopoverDescription>{m.poll_remove_confirm_body()}</PopoverDescription>
                  </PopoverHeader>
                  <div className="mt-3 flex justify-end gap-2">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => setConfirming(false)}
                    >
                      {m.common_cancel()}
                    </Button>
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      onClick={() => {
                        setConfirming(false)
                        onRemove(participant.id)
                      }}
                    >
                      {m.poll_remove_confirm()}
                    </Button>
                  </div>
                </PopoverContent>
              </Popover>
            </span>
          )}
        </div>
      </th>

      {options.map((option) => (
        <td
          key={option.id}
          data-option-id={option.id}
          data-best={option.id === bestOptionId ? 'true' : undefined}
          data-finalized={option.id === finalizedOptionId ? 'true' : undefined}
          className={cn(
            'border-t border-border px-1 py-1.5 transition-colors',
            option.id === bestOptionId && 'bg-accent-soft/35',
            option.id === finalizedOptionId && 'bg-yes-soft/35',
            option.id === hoveredOptionId &&
              option.id !== bestOptionId &&
              option.id !== finalizedOptionId &&
              'bg-secondary/60',
          )}
        >
          <VoteCell
            readOnly
            answer={participant.votes[option.id] ?? null}
            allowIfNeedBe={allowIfNeedBe}
            optionLabel={`${participant.name}, ${optionLabels[option.id] ?? ''}`}
          />
        </td>
      ))}
    </motion.tr>
  )
}
