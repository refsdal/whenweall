import { useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { X } from 'lucide-react'
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
import { listItem, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'

export type SlotClaimant = { participantId: string; name: string; isYou: boolean }

/** Initials for the little avatar dot on a claimant chip. */
function initial(name: string): string {
  return name.trim().slice(0, 1).toUpperCase() || '?'
}

function RemoveClaimant({ claimant, onRemove }: { claimant: SlotClaimant; onRemove: () => void }) {
  const [confirming, setConfirming] = useState(false)

  return (
    <Popover open={confirming} onOpenChange={setConfirming}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={m.signup_remove_claimant({ name: claimant.name })}
          className="-mr-1 size-5 rounded-full text-muted-foreground hover:text-destructive"
        >
          <X aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64">
        <PopoverHeader>
          <PopoverTitle>{m.signup_remove_confirm_title({ name: claimant.name })}</PopoverTitle>
          <PopoverDescription>{m.signup_remove_confirm_body()}</PopoverDescription>
        </PopoverHeader>
        <div className="mt-3 flex justify-end gap-2">
          <Button type="button" variant="ghost" size="sm" onClick={() => setConfirming(false)}>
            {m.common_cancel()}
          </Button>
          <Button
            type="button"
            variant="destructive"
            size="sm"
            onClick={() => {
              setConfirming(false)
              onRemove()
            }}
          >
            {m.signup_remove_confirm()}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}

/**
 * Who has taken this slot: one chip per person, yours marked. The organiser gets a small ✕ on
 * every chip — freeing a spot is a normal part of running a sheet, but it deletes someone else's
 * sign-up, so it asks first.
 */
export function ClaimantList({
  claimants,
  isOwner,
  onRemove,
}: {
  claimants: SlotClaimant[]
  isOwner: boolean
  onRemove: (participantId: string) => void
}) {
  const reduceMotion = useReducedMotion()

  if (claimants.length === 0) {
    return <p className="text-xs text-muted-foreground">{m.signup_claimants_empty()}</p>
  }

  return (
    <ul data-testid="claimant-list" className="flex flex-wrap gap-1.5">
      <AnimatePresence initial={false}>
        {claimants.map((claimant) => (
          <motion.li
            key={claimant.participantId}
            layout={reduceMotion ? false : 'position'}
            initial={reduceMotion ? false : listItem.initial}
            animate={listItem.animate}
            exit={reduceMotion ? { opacity: 0 } : listItem.exit}
            transition={listItem.transition}
            data-testid={`claimant-${claimant.participantId}`}
            className={cn(
              'flex max-w-full items-center gap-1.5 rounded-full py-0.5 pr-2 pl-0.5 text-xs',
              claimant.isYou
                ? 'bg-accent-soft text-accent-foreground'
                : 'bg-secondary text-secondary-foreground',
            )}
          >
            <span
              aria-hidden="true"
              className={cn(
                'inline-flex size-5 shrink-0 items-center justify-center rounded-full text-[0.5625rem] font-semibold',
                claimant.isYou
                  ? 'bg-primary-strong text-primary-foreground'
                  : 'bg-card text-muted-foreground',
              )}
            >
              {initial(claimant.name)}
            </span>
            <span className="min-w-0 truncate" title={claimant.name}>
              {claimant.name}
            </span>
            {claimant.isYou && (
              <span className="shrink-0 text-[0.625rem] font-semibold uppercase">
                {m.poll_you_badge()}
              </span>
            )}
            {isOwner && (
              <RemoveClaimant
                claimant={claimant}
                onRemove={() => onRemove(claimant.participantId)}
              />
            )}
          </motion.li>
        ))}
      </AnimatePresence>
    </ul>
  )
}
