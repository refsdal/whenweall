import { localToUtcIso } from '#/lib/time'

export type Interval = { start: string; end: string } // UTC ISO

export type PageRules = {
  timezone: string
  slotDurationMin: number
  bufferBeforeMin: number
  bufferAfterMin: number
  minNoticeMin: number
  maxDaysAhead: number
  availability: Record<string, { start: string; end: string }[]>
  dateOverrides: Record<string, { start: string; end: string }[]> | null
}

function localDateStr(d: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(d)
}

function addDaysToDateStr(dateStr: string, days: number): string {
  const [y, m, d] = dateStr.split('-').map(Number)
  const dt = new Date(Date.UTC(y, m - 1, d))
  dt.setUTCDate(dt.getUTCDate() + days)
  return dt.toISOString().slice(0, 10)
}

function daysBetween(from: string, to: string): number {
  const a = new Date(`${from}T00:00:00Z`).getTime()
  const b = new Date(`${to}T00:00:00Z`).getTime()
  return Math.round((b - a) / 86_400_000)
}

function weekdayKey(dateStr: string): string {
  return String(new Date(`${dateStr}T00:00:00Z`).getUTCDay())
}

function toMinutes(hhmm: string): number {
  const [h, m] = hhmm.split(':').map(Number)
  return h * 60 + m
}

function fromMinutes(min: number): string {
  const h = Math.floor(min / 60)
  const m = min % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

function overlaps(aStart: number, aEnd: number, bStart: number, bEnd: number): boolean {
  return aStart < bEnd && bStart < aEnd
}

/**
 * Generates the set of bookable UTC slots for a page over [from, to] (local calendar dates in
 * the page timezone, inclusive), honouring weekly availability, date overrides (which fully
 * replace the weekly ranges for that local date; an empty array means the day is off), slot
 * duration, buffers, notice, horizon and busy intervals. Pure and deterministic: sorted, deduped.
 */
export function generateSlots(
  rules: PageRules,
  opts: { from: Date; to: Date; now: Date; busy: Interval[] },
): Interval[] {
  const {
    timezone,
    slotDurationMin,
    bufferBeforeMin,
    bufferAfterMin,
    minNoticeMin,
    maxDaysAhead,
    availability,
    dateOverrides,
  } = rules

  const fromDateStr = localDateStr(opts.from, timezone)
  const toDateStr = localDateStr(opts.to, timezone)
  const spanDays = daysBetween(fromDateStr, toDateStr)
  if (spanDays < 0) return []

  const nowMs = opts.now.getTime()
  const earliestStartMs = nowMs + minNoticeMin * 60_000
  const latestEndMs = nowMs + maxDaysAhead * 86_400_000

  const busy = opts.busy.map((b) => ({
    start: new Date(b.start).getTime(),
    end: new Date(b.end).getTime(),
  }))

  const seen = new Set<string>()
  const results: Interval[] = []

  for (let i = 0; i <= spanDays; i++) {
    const dateStr = addDaysToDateStr(fromDateStr, i)
    const ranges = dateOverrides?.[dateStr] ?? availability[weekdayKey(dateStr)] ?? []

    for (const range of ranges) {
      const rangeStart = toMinutes(range.start)
      const rangeEnd = toMinutes(range.end)

      for (let s = rangeStart; s + slotDurationMin <= rangeEnd; s += slotDurationMin) {
        const startIso = localToUtcIso(dateStr, fromMinutes(s), timezone)
        const endIso = localToUtcIso(dateStr, fromMinutes(s + slotDurationMin), timezone)
        const startMs = new Date(startIso).getTime()
        const endMs = new Date(endIso).getTime()

        if (startMs < earliestStartMs) continue
        if (endMs > latestEndMs) continue

        const bufStart = startMs - bufferBeforeMin * 60_000
        const bufEnd = endMs + bufferAfterMin * 60_000
        const conflicts = busy.some((b) => overlaps(bufStart, bufEnd, b.start, b.end))
        if (conflicts) continue

        const key = `${startIso}|${endIso}`
        if (seen.has(key)) continue
        seen.add(key)
        results.push({ start: startIso, end: endIso })
      }
    }
  }

  results.sort((a, b) => a.start.localeCompare(b.start))
  return results
}

/**
 * Cheap, consistent single-slot check: generates slots over a one-day window around `startAt`
 * and looks for an exact start match.
 */
export function isSlotAvailable(
  rules: PageRules,
  startAt: string,
  opts: { now: Date; busy: Interval[] },
): boolean {
  const startMs = new Date(startAt).getTime()
  const from = new Date(startMs - 86_400_000)
  const to = new Date(startMs + 86_400_000)
  return generateSlots(rules, { from, to, now: opts.now, busy: opts.busy }).some(
    (s) => s.start === startAt,
  )
}
