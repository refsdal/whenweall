import { useState, type Dispatch } from 'react'
import { CalendarOff, Clock, Plus, X } from 'lucide-react'
import { Badge } from '#/components/ui/badge'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import {
  nextRange,
  type DayIssue,
  type DayIssueCode,
  type DraftRange,
  type EditorAction,
} from '#/components/booking/editor-state'
import { getLocale, intlLocale, m } from '#/lib/i18n'
import { LIMITS } from '#/api/bookings'

const DEFAULT_OVERRIDE_RANGE: DraftRange = { start: '09:00', end: '17:00' }

/** `2026-09-01` → "1 September 2026", formatted in UTC so server and client always agree. */
function formatDate(date: string, locale: string): string {
  return new Intl.DateTimeFormat(intlLocale(locale), {
    dateStyle: 'long',
    timeZone: 'UTC',
  }).format(new Date(`${date}T00:00:00Z`))
}

function issueMessage(code: DayIssueCode): string {
  if (code === 'overlap') return m.booking_availability_issue_overlap()
  if (code === 'order') return m.booking_availability_issue_order()
  return m.booking_availability_issue_unaligned()
}

function OverrideRow({
  date,
  ranges,
  issues,
  locale,
  dispatch,
}: {
  date: string
  ranges: DraftRange[]
  issues: DayIssue[]
  locale: string
  dispatch: Dispatch<EditorAction>
}) {
  const day = formatDate(date, locale)
  const flagged = new Set(issues.map((issue) => issue.index))
  const codes = [...new Set(issues.map((issue) => issue.code))]

  function setRanges(next: DraftRange[]) {
    dispatch({ type: 'setOverride', date, ranges: next })
  }

  return (
    <li className="flex flex-col gap-2 py-3 sm:flex-row sm:items-start sm:gap-4">
      <div className="flex items-center gap-2 sm:w-48 sm:shrink-0 sm:pt-1.5">
        <span className="text-sm font-medium">{day}</span>
      </div>

      <div className="flex min-w-0 flex-1 flex-col gap-2">
        {ranges.length === 0 ? (
          <Badge variant="secondary" className="w-fit gap-1">
            <CalendarOff aria-hidden="true" />
            {m.booking_overrides_day_off()}
          </Badge>
        ) : (
          <ul className="flex flex-col gap-2">
            {ranges.map((range, index) => (
              <li key={index} className="flex flex-wrap items-center gap-1.5">
                <Input
                  type="time"
                  step={900}
                  value={range.start}
                  aria-label={m.booking_availability_start({ day })}
                  aria-invalid={flagged.has(index) || undefined}
                  onChange={(event) =>
                    setRanges(
                      ranges.map((r, i) => (i === index ? { ...r, start: event.target.value } : r)),
                    )
                  }
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
                  onChange={(event) =>
                    setRanges(
                      ranges.map((r, i) => (i === index ? { ...r, end: event.target.value } : r)),
                    )
                  }
                  className="h-9 w-[7.5rem] tabular-nums"
                />
                <button
                  type="button"
                  aria-label={m.booking_availability_remove({
                    day,
                    start: range.start,
                    end: range.end,
                  })}
                  onClick={() => setRanges(ranges.filter((_, i) => i !== index))}
                  className="focus-ring inline-flex size-8 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-black/5 hover:text-foreground dark:hover:bg-white/10"
                >
                  <X aria-hidden="true" className="size-3.5" />
                </button>
              </li>
            ))}
          </ul>
        )}

        {ranges.length > 0 && (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="w-fit"
            aria-label={m.booking_availability_add_for({ day })}
            onClick={() => {
              const next = nextRange(ranges)
              if (next) setRanges([...ranges, next])
            }}
          >
            <Plus aria-hidden="true" />
            {m.booking_availability_add()}
          </Button>
        )}

        {codes.map((code) => (
          <p key={code} className="text-sm text-destructive">
            {issueMessage(code)}
          </p>
        ))}
      </div>

      <button
        type="button"
        aria-label={m.booking_overrides_remove({ date: day })}
        onClick={() => dispatch({ type: 'setOverride', date, ranges: null })}
        className="focus-ring inline-flex size-8 shrink-0 items-center justify-center self-start rounded-full text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
      >
        <X aria-hidden="true" className="size-4" />
      </button>
    </li>
  )
}

/**
 * The days that break the weekly pattern: a holiday off, or extra hours one Saturday. An override
 * fully replaces the weekly hours for its date — an empty list of windows is a deliberate day off.
 */
export function DateOverridesEditor({
  overrides,
  issues,
  dispatch,
}: {
  overrides: Record<string, DraftRange[]>
  issues: Record<string, DayIssue[]>
  dispatch: Dispatch<EditorAction>
}) {
  const locale = getLocale()
  const [date, setDate] = useState('')
  const [error, setError] = useState<string | null>(null)

  const dates = Object.keys(overrides).sort()
  const full = dates.length >= LIMITS.overrideDays

  function add(ranges: DraftRange[]) {
    if (!date) return
    if (date in overrides) {
      setError(m.booking_overrides_exists())
      return
    }
    if (full) {
      setError(m.booking_overrides_limit())
      return
    }
    setError(null)
    dispatch({ type: 'setOverride', date, ranges })
    setDate('')
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-muted-foreground">{m.booking_overrides_hint()}</p>

      <div className="flex flex-wrap items-end gap-2">
        <div className="flex flex-col gap-1">
          <Label htmlFor="override-date" className="text-xs text-muted-foreground">
            {m.booking_overrides_date_label()}
          </Label>
          <Input
            id="override-date"
            type="date"
            value={date}
            onChange={(event) => {
              setDate(event.target.value)
              setError(null)
            }}
            className="h-9 w-[10.5rem]"
          />
        </div>
        <Button type="button" variant="outline" size="sm" disabled={!date} onClick={() => add([])}>
          <CalendarOff aria-hidden="true" />
          {m.booking_overrides_add_off()}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={!date}
          onClick={() => add([{ ...DEFAULT_OVERRIDE_RANGE }])}
        >
          <Clock aria-hidden="true" />
          {m.booking_overrides_add_hours()}
        </Button>
      </div>

      {error && <p className="text-sm text-destructive">{error}</p>}

      {dates.length === 0 ? (
        <p className="text-sm text-muted-foreground">{m.booking_overrides_empty()}</p>
      ) : (
        <ul className="divide-y divide-border">
          {dates.map((key) => (
            <OverrideRow
              key={key}
              date={key}
              ranges={overrides[key] ?? []}
              issues={issues[key] ?? []}
              locale={locale}
              dispatch={dispatch}
            />
          ))}
        </ul>
      )}
    </div>
  )
}
