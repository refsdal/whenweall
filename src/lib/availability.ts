import { localToUtcIso } from '#/lib/time'

// Policy: slots are laid on a UTC grid anchored at each local range start, not on a per-slot
// local-time grid. Only the range's start and end local wall-clock times are converted to UTC
// (once each, via TZDate through localToUtcIso); every slot within the range is then
// `rangeStartUtc + k*durationMs .. +duration` in pure UTC arithmetic. Local wall-clock spacing
// between consecutive slots may therefore shift by an hour on a DST transition date (e.g. a
// range that reads "01:00-04:00" local may straddle a jump straight from 02:00 to 03:00) —
// that's intentional: it keeps every emitted slot's duration exactly `slotDurationMin` and its
// ordering strictly increasing, which a naive per-slot local-time conversion cannot guarantee
// around a nonexistent (spring-forward) or ambiguous (fall-back) local hour.
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

    const durationMs = slotDurationMin * 60_000

    for (const range of ranges) {
      // Convert only the range boundaries to UTC (once each) — see the file-header policy note.
      const rangeStartMs = new Date(localToUtcIso(dateStr, range.start, timezone)).getTime()
      const rangeEndMs = new Date(localToUtcIso(dateStr, range.end, timezone)).getTime()
      // A range whose start/end both fall inside a spring-forward gap (or otherwise get mapped
      // to a non-increasing UTC order) can't be laid out on a grid; skip it rather than emit
      // nonsense.
      if (!(rangeStartMs < rangeEndMs)) continue

      for (let startMs = rangeStartMs; startMs + durationMs <= rangeEndMs; startMs += durationMs) {
        const endMs = startMs + durationMs
        // Safety net: every slot must have exactly the configured duration.
        if (endMs - startMs !== durationMs) continue

        if (startMs < earliestStartMs) continue
        if (endMs > latestEndMs) continue

        const bufStart = startMs - bufferBeforeMin * 60_000
        const bufEnd = endMs + bufferAfterMin * 60_000
        const conflicts = busy.some((b) => overlaps(bufStart, bufEnd, b.start, b.end))
        if (conflicts) continue

        const startIso = new Date(startMs).toISOString()
        const endIso = new Date(endMs).toISOString()
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
