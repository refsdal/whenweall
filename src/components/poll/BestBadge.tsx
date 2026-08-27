import { motion } from 'motion/react'
import { Crown } from 'lucide-react'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'

/**
 * A grid shows exactly one crown at a time — `best` only applies while nothing is finalized — so
 * every badge can share a layout id and the crown physically slides from column to column as the
 * lead changes, instead of blinking out of one header and into another.
 */
const CROWN_LAYOUT_ID = 'poll-crown'

/** Crowns the column that is winning right now (or the one the organiser picked). */
export function BestBadge({
  variant = 'best',
  className,
}: {
  variant?: 'best' | 'picked'
  className?: string
}) {
  const reduceMotion = useReducedMotion()

  return (
    <motion.span
      layoutId={reduceMotion ? undefined : CROWN_LAYOUT_ID}
      transition={spring}
      className={cn(
        'inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[0.5625rem] font-semibold tracking-wide uppercase',
        variant === 'picked'
          ? 'bg-yes-soft text-yes-ink'
          : 'bg-primary-strong text-primary-foreground',
        className,
      )}
    >
      <Crown aria-hidden="true" className="size-2.5" />
      {variant === 'picked' ? m.poll_picked_badge() : m.poll_best_badge()}
    </motion.span>
  )
}
