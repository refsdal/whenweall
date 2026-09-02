import { useMemo, useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { MonthPicker } from '#/components/booking/MonthPicker'
import { SlotList } from '#/components/booking/SlotList'
import type { Interval } from '#/lib/availability'
import { getLocale, intlLocale, m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'

/**
 * Slots come off the server as UTC instants; a visitor thinks in their own days. Grouping is
 * therefore done here, in the viewer's timezone — which is also why the loader asks for a day of
 * padding either side of the month it renders.
 */
export function groupSlotsByDay(slots: Interval[], timeZone: string): Record<string, Interval[]> {
  const format = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
  const grouped: Record<string, Interval[]> = {}
  for (const slot of slots) {
    const key = format.format(new Date(slot.start))
    ;(grouped[key] ??= []).push(slot)
  }
  return grouped
}

/**
 * "Monday 15 September" for a `YYYY-MM-DD` key. The key already *is* a local calendar day (the
 * grouping above put it in the viewer's zone), so it is formatted as UTC — re-projecting it into
 * a zone would shift the label by a day at the extremes (UTC+14, UTC-11).
 */
function formatDay(day: string, locale: string): string {
  const [y, mo, d] = day.split('-').map(Number) as [number, number, number]
  return new Intl.DateTimeFormat(intlLocale(locale), {
    timeZone: 'UTC',
    weekday: 'long',
    day: 'numeric',
    month: 'long',
  }).format(new Date(Date.UTC(y, mo - 1, d)))
}

/**
 * Month on one side, the free times on the chosen day on the other — the whole visitor-facing
 * choice in one component, shared by the public page and the reschedule dialog.
 *
 * The month is controlled by the caller (it lives in the URL so the loader can fetch it); the
 * chosen day is local state, and falls back to the first open day of the month so a visitor who
 * lands here can pick a time without picking a day first.
 */
export function SlotPicker({
  slots,
  timeZone,
  month,
  onMonthChange,
  minMonth,
  maxMonth,
  selectedStart,
  onPick,
  className,
}: {
  slots: Interval[]
  timeZone: string
  month: string
  onMonthChange: (month: string) => void
  minMonth?: string
  maxMonth?: string
  selectedStart?: string | null
  onPick: (slot: Interval) => void
  className?: string
}) {
  const locale = getLocale()
  const reduceMotion = useReducedMotion()
  const [chosenDay, setChosenDay] = useState<string | null>(null)

  const byDay = useMemo(() => groupSlotsByDay(slots, timeZone), [slots, timeZone])
  const availableDays = useMemo(
    () =>
      Object.keys(byDay)
        .filter((day) => day.startsWith(month))
        .sort(),
    [byDay, month],
  )

  // Derived rather than stored in an effect: a month change (or a slot someone else just took)
  // must never leave the list pointing at a day that no longer has anything on it.
  const day =
    chosenDay && availableDays.includes(chosenDay) ? chosenDay : (availableDays[0] ?? null)

  return (
    <div className={cn('grid gap-6 sm:grid-cols-[auto_minmax(0,1fr)]', className)}>
      <div className="surface w-full p-3 sm:w-fit">
        <h2 className="sr-only">{m.book_public_pick_day()}</h2>
        <MonthPicker
          month={month}
          availableDays={availableDays}
          selected={day}
          onSelect={setChosenDay}
          onMonthChange={(next) => {
            setChosenDay(null)
            onMonthChange(next)
          }}
          minMonth={minMonth}
          maxMonth={maxMonth}
        />
      </div>

      <div className="flex min-w-0 flex-col gap-3">
        {day === null ? (
          <p className="rounded-xl border border-dashed border-border px-6 py-10 text-center text-sm text-balance text-muted-foreground">
            {m.book_public_no_slots_month()}
          </p>
        ) : (
          <>
            <h2 className="text-sm font-medium first-letter:uppercase">
              {m.book_public_slots_on({ day: formatDay(day, locale) })}
            </h2>
            <AnimatePresence mode="wait" initial={false}>
              <motion.div
                key={day}
                initial={reduceMotion ? false : { opacity: 0, y: 6 }}
                animate={{ opacity: 1, y: 0 }}
                exit={reduceMotion ? undefined : { opacity: 0, y: -6 }}
                transition={spring}
              >
                <SlotList
                  day={day}
                  slots={byDay[day] ?? []}
                  timeZone={timeZone}
                  selected={selectedStart}
                  onPick={onPick}
                />
              </motion.div>
            </AnimatePresence>
          </>
        )}
      </div>
    </div>
  )
}
