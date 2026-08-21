import { Crown } from 'lucide-react'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

/** Crowns the column that is winning right now (or the one the organiser picked). */
export function BestBadge({
  variant = 'best',
  className,
}: {
  variant?: 'best' | 'picked'
  className?: string
}) {
  return (
    <span
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
    </span>
  )
}
