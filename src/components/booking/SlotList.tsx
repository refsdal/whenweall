import { useMemo } from 'react'
import { motion } from 'motion/react'
import { CalendarX } from 'lucide-react'
import type { Interval } from '#/lib/availability'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'

/** 24-hour clock everywhere: both locales this app ships read times that way. */
function timeFormatter(timeZone: string): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat('en-GB', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  })
}

/**
 * The free times on the chosen day, as chips. Start times only — the length is the same for
 * every slot and is stated once above the list — with the full range on each chip's accessible
 * name so a screen reader hears "09:00 to 09:30" rather than a bare number.
 */
export function SlotList({
  day,
  slots,
  timeZone,
  selected,
  onPick,
  className,
}: {
  day: string
  slots: Interval[]
  timeZone: string
  selected?: string | null
  onPick: (slot: Interval) => void
  className?: string
}) {
  const reduceMotion = useReducedMotion()
  const format = useMemo(() => timeFormatter(timeZone), [timeZone])

  if (slots.length === 0) {
    return (
      <p
        data-testid="slot-list-empty"
        className={cn(
          'flex flex-col items-center gap-2 rounded-xl border border-dashed border-border px-6 py-10 text-center text-sm text-balance text-muted-foreground',
          className,
        )}
      >
        <CalendarX aria-hidden="true" className="size-5 opacity-70" />
        {m.book_public_no_slots_day()}
      </p>
    )
  }

  return (
    <ul
      data-testid="slot-list"
      data-day={day}
      className={cn('grid grid-cols-2 gap-2 sm:grid-cols-3', className)}
    >
      {slots.map((slot) => {
        const start = format.format(new Date(slot.start))
        const end = format.format(new Date(slot.end))
        const isSelected = selected === slot.start

        return (
          <li key={slot.start}>
            <motion.button
              type="button"
              onClick={() => onPick(slot)}
              whileTap={reduceMotion ? undefined : { scale: 0.96 }}
              transition={spring}
              aria-pressed={isSelected}
              aria-label={m.book_public_slot_range({ start, end })}
              className={cn(
                'focus-ring w-full cursor-pointer rounded-lg border border-border bg-card py-2.5 text-sm font-medium tabular-nums transition-colors hover:border-primary hover:bg-accent-soft hover:text-accent-foreground',
                isSelected && 'border-primary bg-primary text-primary-foreground',
              )}
            >
              <span suppressHydrationWarning>{start}</span>
            </motion.button>
          </li>
        )
      })}
    </ul>
  )
}
