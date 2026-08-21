import { motion } from 'motion/react'
import { Check, LogOut, Plus } from 'lucide-react'
import { Button } from '#/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '#/components/ui/tooltip'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'

/** Why a slot can't be claimed right now — each maps to the sentence shown in the tooltip. */
export type ClaimBlockedReason = 'full' | 'closed' | 'limit'

export function blockedReasonText(reason: ClaimBlockedReason, maxClaims: number): string {
  if (reason === 'full') return m.signup_reason_full()
  if (reason === 'closed') return m.signup_reason_closed()
  return maxClaims === 1
    ? m.signup_reason_limit_one()
    : m.signup_reason_limit_other({ count: maxClaims })
}

/**
 * The one action on a slot card: take the spot, or give it back.
 *
 * A disabled button says nothing about *why* it's disabled, so the reason travels with it as a
 * tooltip — and, because a disabled button never fires pointer events, the trigger wraps the
 * button in a span that can still be hovered and focused.
 */
export function ClaimButton({
  claimed,
  blockedReason,
  maxClaims,
  pending,
  slotLabel,
  onClaim,
  onLeave,
}: {
  claimed: boolean
  blockedReason: ClaimBlockedReason | null
  maxClaims: number
  pending: boolean
  slotLabel: string
  onClaim: () => void
  onLeave: () => void
}) {
  const reduceMotion = useReducedMotion()
  // Leaving a slot you hold stays possible while it is "full" — you are the reason it is full.
  const disabled = pending || (claimed ? blockedReason === 'closed' : blockedReason !== null)
  const reason = claimed && blockedReason === 'full' ? null : blockedReason

  const label = claimed
    ? pending
      ? m.signup_leaving()
      : m.signup_leave()
    : pending
      ? m.signup_claiming()
      : m.signup_claim()

  const button = (
    <Button
      type="button"
      size="sm"
      variant={claimed ? 'outline' : 'default'}
      disabled={disabled}
      aria-label={
        claimed
          ? m.signup_leave_aria({ label: slotLabel })
          : m.signup_claim_aria({ label: slotLabel })
      }
      onClick={claimed ? onLeave : onClaim}
      className="w-full"
    >
      {claimed ? (
        <LogOut aria-hidden="true" />
      ) : blockedReason === 'full' ? (
        <Check aria-hidden="true" />
      ) : (
        <Plus aria-hidden="true" />
      )}
      {label}
    </Button>
  )

  const animated = reduceMotion ? (
    button
  ) : (
    <motion.div whileTap={disabled ? undefined : { scale: 0.96 }} transition={spring}>
      {button}
    </motion.div>
  )

  if (!disabled || reason === null) return animated

  return (
    <TooltipProvider delayDuration={150}>
      <Tooltip>
        <TooltipTrigger asChild>
          {/* A disabled button swallows pointer events, so the tooltip hangs off a wrapper. */}
          <span tabIndex={0} className="focus-ring block rounded-md">
            {animated}
          </span>
        </TooltipTrigger>
        <TooltipContent>{blockedReasonText(reason, maxClaims)}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
