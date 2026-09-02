import type { PageRules } from '#/lib/availability'
import {
  LIMITS,
  createBookingPageSchema,
  updateBookingPageSchema,
  type CreateBookingPageInput,
  type UpdateBookingPageInput,
} from '#/api/bookings'
import type { BookingPageStatus, PageView } from '#/api/types'

/** One window of the day, in the page's own timezone. Mirrors `timeRangeSchema`. */
export type DraftRange = { start: string; end: string }

/** Weekday keys as the availability JSON stores them (`'0'` = Sunday, matching `Date#getDay`). */
export const WEEKDAY_KEYS = ['0', '1', '2', '3', '4', '5', '6'] as const

/** The order weeks are read in here — Monday first, Sunday last. */
export const WEEKDAY_ORDER = ['1', '2', '3', '4', '5', '6', '0'] as const

/**
 * Everything the page editor holds while it is being edited. Mirrors `createBookingPageSchema`
 * plus `status`, but keeps the *editable* shape: text fields are always strings (never
 * `undefined`/`null`), and every weekday has an entry so a day that is off is a real, togglable
 * row rather than a missing key.
 */
export type EditorDraft = {
  slug: string
  title: string
  description: string
  location: string
  timezone: string
  slotDurationMin: number
  bufferBeforeMin: number
  bufferAfterMin: number
  minNoticeMin: number
  maxDaysAhead: number
  availability: Record<string, DraftRange[]>
  dateOverrides: Record<string, DraftRange[]>
  googleSync: boolean
  reminders: boolean
  status: BookingPageStatus
}

export type EditorAction =
  | { type: 'setField'; field: keyof EditorDraft; value: unknown }
  | { type: 'setDayRanges'; weekday: string; ranges: DraftRange[] }
  | { type: 'addRange'; weekday: string }
  | { type: 'removeRange'; weekday: string; index: number }
  | { type: 'copyDayToAll'; weekday: string }
  /** `null` removes the override entirely; `[]` is a deliberate day off. */
  | { type: 'setOverride'; date: string; ranges: DraftRange[] | null }
  | { type: 'reset'; draft: EditorDraft }

const DEFAULT_RANGE: DraftRange = { start: '09:00', end: '17:00' }
const LAST_START = '23:45'
const HHMM_RE = /^([01]\d|2[0-3]):(00|15|30|45)$/

function toMinutes(hhmm: string): number {
  const [h, m] = hhmm.split(':').map(Number)
  return h * 60 + m
}

function toHhmm(minutes: number): string {
  const clamped = Math.max(0, Math.min(minutes, toMinutes(LAST_START)))
  const h = Math.floor(clamped / 60)
  const m = clamped % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

function byStart(a: DraftRange, b: DraftRange): number {
  return a.start === b.start ? a.end.localeCompare(b.end) : a.start.localeCompare(b.start)
}

function emptyWeek(): Record<string, DraftRange[]> {
  return Object.fromEntries(WEEKDAY_KEYS.map((key) => [key, [] as DraftRange[]]))
}

/**
 * A fresh page: weekdays 09:00–17:00, half-hour slots, no buffers, two hours' notice, two months
 * of horizon. `timezone` is passed in rather than read here so the caller controls *when* the
 * browser zone is resolved (never during SSR — that would differ from the server render).
 */
export function initialDraft(timezone: string): EditorDraft {
  const availability = emptyWeek()
  for (const weekday of ['1', '2', '3', '4', '5']) {
    availability[weekday] = [{ ...DEFAULT_RANGE }]
  }

  return {
    slug: '',
    title: '',
    description: '',
    location: '',
    timezone,
    slotDurationMin: 30,
    bufferBeforeMin: 0,
    bufferAfterMin: 0,
    minNoticeMin: LIMITS.minNoticeDefault,
    maxDaysAhead: LIMITS.maxDaysAheadDefault,
    availability,
    dateOverrides: {},
    googleSync: false,
    reminders: true,
    status: 'active',
  }
}

/** The next range to offer on a day: the whole workday when it is off, otherwise an hour tacked
 * on after the last window. Returns `null` when there is no room left in the day. */
export function nextRange(ranges: DraftRange[]): DraftRange | null {
  if (ranges.length === 0) return { ...DEFAULT_RANGE }

  const last = [...ranges].sort(byStart).at(-1) as DraftRange
  const start = last.end
  if (!HHMM_RE.test(start) || toMinutes(start) >= toMinutes(LAST_START)) return null
  return { start, end: toHhmm(toMinutes(start) + 60) }
}

export function editorReducer(draft: EditorDraft, action: EditorAction): EditorDraft {
  switch (action.type) {
    case 'setField':
      return { ...draft, [action.field]: action.value }

    case 'setDayRanges':
      return {
        ...draft,
        availability: { ...draft.availability, [action.weekday]: action.ranges },
      }

    case 'addRange': {
      const ranges = draft.availability[action.weekday] ?? []
      const next = nextRange(ranges)
      if (!next) return draft
      return {
        ...draft,
        availability: { ...draft.availability, [action.weekday]: [...ranges, next] },
      }
    }

    case 'removeRange': {
      const ranges = draft.availability[action.weekday] ?? []
      return {
        ...draft,
        availability: {
          ...draft.availability,
          [action.weekday]: ranges.filter((_, i) => i !== action.index),
        },
      }
    }

    case 'copyDayToAll': {
      const source = draft.availability[action.weekday] ?? []
      const availability = Object.fromEntries(
        WEEKDAY_KEYS.map((key) => [key, source.map((range) => ({ ...range }))]),
      )
      return { ...draft, availability }
    }

    case 'setOverride': {
      const dateOverrides = { ...draft.dateOverrides }
      if (action.ranges === null) delete dateOverrides[action.date]
      else dateOverrides[action.date] = action.ranges
      return { ...draft, dateOverrides }
    }

    case 'reset':
      return action.draft
  }
}

export type DayIssueCode = 'unaligned' | 'order' | 'overlap'
export type DayIssue = { index: number; code: DayIssueCode }

/**
 * What is wrong with one day's windows, reported per range so the editor can point at the row
 * that needs fixing. At most one issue per range: a malformed range can't meaningfully be checked
 * for overlaps too.
 */
export function dayIssues(ranges: DraftRange[]): DayIssue[] {
  const issues: DayIssue[] = []
  const wellFormed: { index: number; start: number; end: number }[] = []

  ranges.forEach((range, index) => {
    if (!HHMM_RE.test(range.start) || !HHMM_RE.test(range.end)) {
      issues.push({ index, code: 'unaligned' })
      return
    }
    const start = toMinutes(range.start)
    const end = toMinutes(range.end)
    if (start >= end) {
      issues.push({ index, code: 'order' })
      return
    }
    wellFormed.push({ index, start, end })
  })

  // Overlaps are reported on the *later* window in clock order, whichever row it was typed into,
  // so "13:00–17:00" then "09:00–14:00" blames the 13:00 row that the 14:00 end runs into.
  const sorted = [...wellFormed].sort((a, b) => a.start - b.start)
  for (let i = 1; i < sorted.length; i++) {
    const prev = sorted[i - 1] as (typeof sorted)[number]
    const cur = sorted[i] as (typeof sorted)[number]
    if (cur.start < prev.end) issues.push({ index: cur.index, code: 'overlap' })
  }

  return issues.sort((a, b) => a.index - b.index)
}

/** Every day (weekday key or override date) that has something wrong with it. */
export function draftIssues(draft: EditorDraft): Record<string, DayIssue[]> {
  const result: Record<string, DayIssue[]> = {}
  for (const [key, ranges] of Object.entries(draft.availability)) {
    const issues = dayIssues(ranges)
    if (issues.length > 0) result[key] = issues
  }
  for (const [date, ranges] of Object.entries(draft.dateOverrides)) {
    const issues = dayIssues(ranges)
    if (issues.length > 0) result[date] = issues
  }
  return result
}

function trimmedOrUndefined(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed.length > 0 ? trimmed : undefined
}

/** Weekly ranges with the off-days dropped and each day sorted, as the server stores them. */
function availabilityFor(draft: EditorDraft): Record<string, DraftRange[]> {
  const result: Record<string, DraftRange[]> = {}
  for (const key of WEEKDAY_KEYS) {
    const ranges = draft.availability[key] ?? []
    if (ranges.length > 0) result[key] = [...ranges].sort(byStart)
  }
  return result
}

function overridesFor(draft: EditorDraft): Record<string, DraftRange[]> | undefined {
  const entries = Object.entries(draft.dateOverrides)
  if (entries.length === 0) return undefined
  return Object.fromEntries(entries.map(([date, ranges]) => [date, [...ranges].sort(byStart)]))
}

/**
 * The `createBookingPage` payload for this draft, or `null` when it wouldn't pass the server's
 * own schema — the editor uses the same `createBookingPageSchema` as the server function does, so
 * the Save button can never send something that is about to bounce.
 */
export function draftToInput(draft: EditorDraft): CreateBookingPageInput | null {
  const availability = availabilityFor(draft)
  // Every day off would publish a page that can never be booked; the schema allows it, the
  // product shouldn't.
  if (Object.keys(availability).length === 0) return null

  const candidate = {
    slug: draft.slug.trim(),
    title: draft.title.trim(),
    description: trimmedOrUndefined(draft.description),
    location: trimmedOrUndefined(draft.location),
    timezone: draft.timezone,
    slotDurationMin: draft.slotDurationMin,
    bufferBeforeMin: draft.bufferBeforeMin,
    bufferAfterMin: draft.bufferAfterMin,
    minNoticeMin: draft.minNoticeMin,
    maxDaysAhead: draft.maxDaysAhead,
    availability,
    dateOverrides: overridesFor(draft),
    googleSync: draft.googleSync,
    reminders: draft.reminders,
  }

  const parsed = createBookingPageSchema.safeParse(candidate)
  return parsed.success ? parsed.data : null
}

/**
 * The `updateBookingPage` payload. Unlike `draftToInput`, an emptied description or location is
 * sent as `''` rather than dropped: on an update an *absent* field means "leave it alone", so
 * dropping it would silently un-clear a field the organiser just emptied out. Same reasoning for
 * `dateOverrides`, which is sent as `{}` once the last override is removed.
 */
export function draftToUpdate(draft: EditorDraft, pageId: string): UpdateBookingPageInput | null {
  const input = draftToInput(draft)
  if (!input) return null

  const parsed = updateBookingPageSchema.safeParse({
    ...input,
    pageId,
    description: draft.description.trim(),
    location: draft.location.trim(),
    dateOverrides: overridesFor(draft) ?? {},
    status: draft.status,
  })
  return parsed.success ? parsed.data : null
}

/** Seeds the editor from an existing page, filling in the days it has no ranges for. */
export function draftFromPage(page: PageView): EditorDraft {
  const availability = emptyWeek()
  for (const [key, ranges] of Object.entries(page.availability)) {
    availability[key] = ranges.map((range) => ({ ...range }))
  }

  return {
    slug: page.slug,
    title: page.title,
    description: page.description ?? '',
    location: page.location ?? '',
    timezone: page.timezone,
    slotDurationMin: page.slotDurationMin,
    bufferBeforeMin: page.bufferBeforeMin,
    bufferAfterMin: page.bufferAfterMin,
    minNoticeMin: page.minNoticeMin,
    maxDaysAhead: page.maxDaysAhead,
    availability,
    dateOverrides: Object.fromEntries(
      Object.entries(page.dateOverrides ?? {}).map(([date, ranges]) => [
        date,
        ranges.map((range) => ({ ...range })),
      ]),
    ),
    googleSync: page.googleSync,
    reminders: page.reminders,
    status: page.status,
  }
}

/** The rules the pure slot generator wants, so the editor can preview its own draft offline. */
export function draftRules(draft: EditorDraft): PageRules {
  return {
    timezone: draft.timezone,
    slotDurationMin: draft.slotDurationMin,
    bufferBeforeMin: draft.bufferBeforeMin,
    bufferAfterMin: draft.bufferAfterMin,
    minNoticeMin: draft.minNoticeMin,
    maxDaysAhead: draft.maxDaysAhead,
    availability: availabilityFor(draft),
    dateOverrides: overridesFor(draft) ?? null,
  }
}

export function canSave(draft: EditorDraft): boolean {
  return draftToInput(draft) !== null
}
