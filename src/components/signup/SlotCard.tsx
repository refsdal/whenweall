import { motion } from 'motion/react'
import { CapacityBar } from '#/components/signup/CapacityBar'
import { ClaimButton, type ClaimBlockedReason } from '#/components/signup/ClaimButton'
import { ClaimantList, type SlotClaimant } from '#/components/signup/ClaimantList'
import type { AppLocale } from '#/app.config'
import { formatOptionLabel } from '#/lib/time'
import { listItem, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'
import type { PollOptionView } from '#/server/polls/viewmodel'

export type { SlotClaimant }

/**
 * One slot: what it is, how full it is, who's on it, and the single button that puts you on or
 * takes you off. Everything it needs is passed in — the card itself performs no requests, which
 * keeps the whole board's state in one place (`useClaims`).
 */
export function SlotCard({
  option,
  locale,
  timeZone,
  count,
  capacity,
  claimants,
  claimedByYou,
  disabledReason,
  maxClaims = 1,
  pending,
  isOwner,
  onClaim,
  onUnclaim,
}: {
  option: PollOptionView
  locale: AppLocale
  timeZone: string
  count: number
  capacity: number | null
  claimants: SlotClaimant[]
  claimedByYou: boolean
  disabledReason: ClaimBlockedReason | null
  maxClaims?: number
  pending: boolean
  isOwner: boolean
  onClaim: (optionId: string) => void
  onUnclaim: (optionId: string, participantId: string) => void
}) {
  const reduceMotion = useReducedMotion()
  const label = formatOptionLabel(option, { locale, timeZone })
  const plainLabel = [label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')
  const full = capacity !== null && count >= capacity
  const yourClaim = claimants.find((claimant) => claimant.isYou)

  return (
    <motion.article
      // No entrance animation: the board is the page's content, and an `initial` opacity would
      // server-render every card invisible until React hydrates. Layout animation only, so cards
      // glide when a live update reorders or resizes them.
      layout={reduceMotion ? false : 'position'}
      transition={listItem.transition}
      data-testid="slot-card"
      data-option-id={option.id}
      data-full={full ? 'true' : undefined}
      className={cn(
        'surface flex flex-col gap-3 p-4 transition-[filter,opacity,box-shadow] duration-300',
        // A slot nobody can take any more steps back visually; the one you hold steps forward.
        full && !claimedByYou && 'opacity-80 saturate-50',
        claimedByYou && 'ring-1 ring-[var(--yes)]',
      )}
    >
      <header className="flex min-w-0 flex-col gap-0.5">
        {label.secondary ? (
          <>
            <span className="text-[0.6875rem] tracking-wide text-muted-foreground uppercase">
              {label.primary}
            </span>
            <span className="text-base font-semibold text-balance">
              {label.secondary}
              {label.tertiary && (
                <span className="ml-1 text-sm font-normal text-muted-foreground">
                  {label.tertiary}
                </span>
              )}
            </span>
          </>
        ) : (
          <span className="text-base font-semibold text-balance">{label.primary}</span>
        )}
      </header>

      <CapacityBar count={count} capacity={capacity} />

      <ClaimantList
        claimants={claimants}
        isOwner={isOwner}
        onRemove={(participantId) => onUnclaim(option.id, participantId)}
      />

      <div className="mt-auto pt-1">
        <ClaimButton
          claimed={claimedByYou}
          blockedReason={disabledReason}
          maxClaims={maxClaims}
          pending={pending}
          slotLabel={plainLabel}
          onClaim={() => onClaim(option.id)}
          onLeave={() => {
            if (yourClaim) onUnclaim(option.id, yourClaim.participantId)
          }}
        />
      </div>
    </motion.article>
  )
}
