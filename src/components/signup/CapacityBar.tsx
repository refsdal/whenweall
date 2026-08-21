import { motion } from 'motion/react'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'

/** "2 of 5 spots", or "3 signed up" when the slot takes as many people as turn up. */
export function capacityText(count: number, capacity: number | null): string {
  if (capacity === null) {
    if (count === 0) return m.signup_spots_none()
    return count === 1 ? m.signup_spots_unlimited_one() : m.signup_spots_unlimited_other({ count })
  }
  return m.signup_spots_taken({ count, capacity })
}

/**
 * How full a slot is: a sentence, and — when the slot has a limit — a bar that fills as people
 * sign up. The bar is decoration for the sentence, so it carries the numbers on a `progressbar`
 * role and the sentence stays the accessible label.
 *
 * Unlimited slots get no bar at all: there is nothing to fill up.
 */
export function CapacityBar({
  count,
  capacity,
  className,
}: {
  count: number
  capacity: number | null
  className?: string
}) {
  const reduceMotion = useReducedMotion()
  const text = capacityText(count, capacity)
  const full = capacity !== null && count >= capacity
  const ratio = capacity === null ? 0 : Math.min(1, capacity === 0 ? 1 : count / capacity)

  return (
    <div data-testid="capacity-bar" className={cn('flex flex-col gap-1.5', className)}>
      <div className="flex items-center justify-between gap-2">
        <span className={cn('text-xs font-medium', full && 'text-muted-foreground')}>{text}</span>
        {full && (
          <span className="rounded-full bg-secondary px-2 py-0.5 text-[0.625rem] font-semibold tracking-wide text-muted-foreground uppercase">
            {m.signup_full()}
          </span>
        )}
      </div>

      {capacity !== null && (
        <div
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={capacity}
          aria-valuenow={Math.min(count, capacity)}
          aria-valuetext={text}
          aria-label={text}
          className="h-1.5 w-full overflow-hidden rounded-full bg-secondary"
        >
          <motion.div
            className={cn('h-full rounded-full', full ? 'bg-muted-foreground/60' : 'bg-yes')}
            initial={reduceMotion ? false : { width: 0 }}
            animate={{ width: `${ratio * 100}%` }}
            transition={reduceMotion ? { duration: 0 } : spring}
          />
        </div>
      )}
    </div>
  )
}
