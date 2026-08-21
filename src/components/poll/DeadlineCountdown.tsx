import { Clock } from 'lucide-react'
import type { AppLocale } from '#/app.config'
import { getLocale, m } from '#/lib/i18n'
import { useNow } from '#/lib/use-now'
import { cn } from '#/lib/utils'

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

/**
 * Time left, at the coarsest useful resolution: "2d 4h", "4h 5m", "35m", then a phrase for the
 * final minute. Pure so the wording can be tested without a clock.
 */
export function formatCountdown(ms: number, locale: AppLocale): string {
  if (ms <= 0) return m.poll_deadline_closed({}, { locale })

  const days = Math.floor(ms / DAY)
  const hours = Math.floor((ms % DAY) / HOUR)
  const minutes = Math.floor((ms % HOUR) / MINUTE)

  if (days > 0) return m.poll_countdown_dh({ days, hours }, { locale })
  if (hours > 0) return m.poll_countdown_hm({ hours, minutes }, { locale })
  if (minutes > 0) return m.poll_countdown_m({ minutes }, { locale })
  return m.poll_countdown_soon({}, { locale })
}

/**
 * "Closes in 2d 4h" — ticking down once a minute.
 *
 * `suppressHydrationWarning` covers the seconds of drift between the server's clock and the
 * browser's: both render the same coarse label in practice, and a mismatch here is cosmetic.
 */
export function DeadlineCountdown({
  deadlineAt,
  className,
}: {
  deadlineAt: string
  className?: string
}) {
  const locale = getLocale()
  const now = useNow(30_000)
  const remaining = new Date(deadlineAt).getTime() - now.getTime()
  const expired = remaining <= 0
  const text = formatCountdown(remaining, locale)

  return (
    <span
      suppressHydrationWarning
      title={new Date(deadlineAt).toLocaleString()}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium',
        expired ? 'bg-secondary text-muted-foreground' : 'bg-accent-soft text-accent-foreground',
        className,
      )}
    >
      <Clock aria-hidden="true" className="size-3.5" />
      {expired ? text : m.poll_deadline_in({ time: text })}
    </span>
  )
}
