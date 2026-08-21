import { useMemo } from 'react'
import { enGB, nb } from 'date-fns/locale'
import { Calendar } from '#/components/ui/calendar'
import { getLocale } from '#/lib/i18n'
import { cn } from '#/lib/utils'

/** Local calendar day → the `YYYY-MM-DD` key slots are grouped under (never a UTC shift). */
export function dayKey(date: Date): string {
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

/** `YYYY-MM-DD` → a `Date` at local midnight, the calendar's own idea of a day. */
export function dayFromKey(key: string): Date {
  const [y, mo, d] = key.split('-').map(Number) as [number, number, number]
  return new Date(y, mo - 1, d)
}

export function monthKey(date: Date): string {
  return `${date.getFullYear()}-${`${date.getMonth() + 1}`.padStart(2, '0')}`
}

export function monthFromKey(key: string): Date {
  const [y, mo] = key.split('-').map(Number) as [number, number]
  return new Date(y, mo - 1, 1)
}

/**
 * The month view a visitor picks a day from: only days that still have a free slot are
 * clickable, and they carry a dot so the open days are readable at a glance.
 *
 * The month is controlled by the caller (it lives in the URL, so the loader can fetch exactly
 * that window), as is the selection. Day keys are plain `YYYY-MM-DD` strings already grouped in
 * the visitor's timezone — this component never does timezone math itself.
 */
export function MonthPicker({
  month,
  availableDays,
  selected,
  onSelect,
  onMonthChange,
  minMonth,
  maxMonth,
  className,
}: {
  month: string
  availableDays: string[]
  selected: string | null
  onSelect: (day: string) => void
  onMonthChange: (month: string) => void
  minMonth?: string
  maxMonth?: string
  className?: string
}) {
  const locale = getLocale()
  const available = useMemo(() => new Set(availableDays), [availableDays])
  const availableDates = useMemo(() => availableDays.map(dayFromKey), [availableDays])

  return (
    <Calendar
      mode="single"
      month={monthFromKey(month)}
      onMonthChange={(next) => onMonthChange(monthKey(next))}
      startMonth={minMonth ? monthFromKey(minMonth) : undefined}
      endMonth={maxMonth ? monthFromKey(maxMonth) : undefined}
      locale={locale === 'nb' ? nb : enGB}
      showOutsideDays={false}
      selected={selected ? dayFromKey(selected) : undefined}
      disabled={(date: Date) => !available.has(dayKey(date))}
      modifiers={{ available: availableDates }}
      modifiersClassNames={{
        available:
          'font-semibold after:absolute after:bottom-1 after:left-1/2 after:size-1 after:-translate-x-1/2 after:rounded-full after:bg-primary after:content-[""] data-[selected=true]:after:bg-primary-foreground',
      }}
      onSelect={(_selected, triggerDate) => {
        if (!available.has(dayKey(triggerDate))) return
        onSelect(dayKey(triggerDate))
      }}
      className={cn('w-full bg-transparent p-0 [--cell-size:--spacing(9)]', className)}
    />
  )
}
