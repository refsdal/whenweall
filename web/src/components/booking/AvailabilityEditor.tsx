import type { Dispatch } from 'react'
import { CopyPlus, Plus, X } from 'lucide-react'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Switch } from '#/components/ui/switch'
import {
  WEEKDAY_ORDER,
  type DayIssue,
  type DayIssueCode,
  type DraftRange,
  type EditorAction,
} from '#/components/booking/editor-state'
import { getLocale, intlLocale, m } from '#/lib/i18n'

/** 2024-01-07 was a Sunday, so `7 + weekday` lands on the day the `'0'..'6'` key means. */
function weekdayName(weekday: string, locale: string): string {
  const date = new Date(Date.UTC(2024, 0, 7 + Number(weekday)))
  return new Intl.DateTimeFormat(intlLocale(locale), { weekday: 'long', timeZone: 'UTC' }).format(
    date,
  )
}

/** Norwegian weekday names come back lowercase; a row heading wants a capital. */
function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

function issueMessage(code: DayIssueCode): string {
  if (code === 'overlap') return m.booking_availability_issue_overlap()
  if (code === 'order') return m.booking_availability_issue_order()
  return m.booking_availability_issue_unaligned()
}

function DayRow({
  weekday,
  ranges,
  issues,
  dispatch,
  locale,
}: {
  weekday: string
  ranges: DraftRange[]
  issues: DayIssue[]
  dispatch: Dispatch<EditorAction>
  locale: string
}) {
  const day = weekdayName(weekday, locale)
  const open = ranges.length > 0
  const flagged = new Set(issues.map((issue) => issue.index))
  // One line per distinct problem: three overlapping windows shouldn't say the same thing thrice.
  const codes = [...new Set(issues.map((issue) => issue.code))]

  function setRange(index: number, patch: Partial<DraftRange>) {
    dispatch({
      type: 'setDayRanges',
      weekday,
      ranges: ranges.map((range, i) => (i === index ? { ...range, ...patch } : range)),
    })
  }

  function toggle(next: boolean) {
    if (next) dispatch({ type: 'addRange', weekday })
    else dispatch({ type: 'setDayRanges', weekday, ranges: [] })
  }

  return (
    <li className="flex flex-col gap-2.5 py-3 sm:flex-row sm:items-start sm:gap-4">
      <div className="flex items-center gap-2.5 sm:w-36 sm:shrink-0 sm:pt-2">
        <Switch
          checked={open}
          onCheckedChange={toggle}
          aria-label={m.booking_availability_day_toggle({ day })}
        />
        <span className="text-sm font-medium">{capitalize(day)}</span>
      </div>

      <div className="flex min-w-0 flex-1 flex-col gap-2">
        {open ? (
          <ul className="flex flex-col gap-2">
            {ranges.map((range, index) => (
              <li key={index} className="flex flex-wrap items-center gap-1.5">
                <Input
                  type="time"
                  step={900}
                  value={range.start}
                  aria-label={m.booking_availability_start({ day })}
                  aria-invalid={flagged.has(index) || undefined}
                  onChange={(event) => setRange(index, { start: event.target.value })}
                  className="h-9 w-[7.5rem] tabular-nums"
                />
                <span aria-hidden="true" className="text-muted-foreground">
                  –
                </span>
                <Input
                  type="time"
                  step={900}
                  value={range.end}
                  aria-label={m.booking_availability_end({ day })}
                  aria-invalid={flagged.has(index) || undefined}
                  onChange={(event) => setRange(index, { end: event.target.value })}
                  className="h-9 w-[7.5rem] tabular-nums"
                />
                <button
                  type="button"
                  aria-label={m.booking_availability_remove({
                    day,
                    start: range.start,
                    end: range.end,
                  })}
                  onClick={() => dispatch({ type: 'removeRange', weekday, index })}
                  className="focus-ring inline-flex size-8 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-black/5 hover:text-foreground dark:hover:bg-white/10"
                >
                  <X aria-hidden="true" className="size-3.5" />
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground sm:pt-2">
            {m.booking_availability_day_off()}
          </p>
        )}

        <div className="flex flex-wrap items-center gap-1">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            aria-label={m.booking_availability_add_for({ day })}
            onClick={() => dispatch({ type: 'addRange', weekday })}
          >
            <Plus aria-hidden="true" />
            {m.booking_availability_add()}
          </Button>
          {open && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-label={m.booking_availability_copy_from({ day })}
              onClick={() => dispatch({ type: 'copyDayToAll', weekday })}
            >
              <CopyPlus aria-hidden="true" />
              {m.booking_availability_copy_to_all()}
            </Button>
          )}
        </div>

        {codes.map((code) => (
          <p key={code} className="text-sm text-destructive">
            {issueMessage(code)}
          </p>
        ))}
      </div>
    </li>
  )
}

/**
 * The weekly grid: one row per weekday (Monday first), each a toggle plus its time windows.
 * Purely controlled — every edit goes back out as an `EditorAction`, so the reducer stays the
 * single source of truth for what a day looks like.
 */
export function AvailabilityEditor({
  availability,
  issues,
  timezone,
  dispatch,
}: {
  availability: Record<string, DraftRange[]>
  issues: Record<string, DayIssue[]>
  timezone: string
  dispatch: Dispatch<EditorAction>
}) {
  const locale = getLocale()
  const allOff = WEEKDAY_ORDER.every((weekday) => (availability[weekday] ?? []).length === 0)

  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm text-muted-foreground">
        {m.booking_availability_hint({ timezone: timezone.replace(/_/g, ' ') })}
      </p>

      <ul className="divide-y divide-border">
        {WEEKDAY_ORDER.map((weekday) => (
          <DayRow
            key={weekday}
            weekday={weekday}
            ranges={availability[weekday] ?? []}
            issues={issues[weekday] ?? []}
            dispatch={dispatch}
            locale={locale}
          />
        ))}
      </ul>

      {allOff && <p className="text-sm text-destructive">{m.booking_availability_empty()}</p>}
    </div>
  )
}
