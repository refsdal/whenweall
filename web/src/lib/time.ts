import { TZDate } from '@date-fns/tz'

function localeFor(locale: string): string {
  if (locale === 'en') return 'en'
  if (locale === 'nb') return 'nb-NO'
  return locale
}

export function localToUtcIso(date: string, time: string, timeZone: string): string {
  const [y, m, d] = date.split('-').map(Number)
  const [hh, mm] = time.split(':').map(Number)
  const zoned = new TZDate(y, m - 1, d, hh, mm, 0, timeZone)
  return new Date(zoned.getTime()).toISOString()
}

export function utcIsoToLocalParts(iso: string, timeZone: string): { date: string; time: string } {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(new Date(iso))

  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? ''

  const date = `${get('year')}-${get('month')}-${get('day')}`
  const time = `${get('hour')}:${get('minute')}`
  return { date, time }
}

function weekdayShort(d: Date, locale: string, timeZone: string): string {
  const parts = new Intl.DateTimeFormat(locale, { timeZone, weekday: 'short' }).formatToParts(d)
  return parts.find((p) => p.type === 'weekday')?.value ?? ''
}

function dayMonth(d: Date, locale: string, timeZone: string): string {
  const parts = new Intl.DateTimeFormat(locale, {
    timeZone,
    day: 'numeric',
    month: 'short',
  }).formatToParts(d)
  const day = parts.find((p) => p.type === 'day')?.value ?? ''
  const month = parts.find((p) => p.type === 'month')?.value ?? ''
  return `${day} ${month}`
}

function hhmm(d: Date, timeZone: string): string {
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(d)
  const hour = parts.find((p) => p.type === 'hour')?.value ?? ''
  const minute = parts.find((p) => p.type === 'minute')?.value ?? ''
  return `${hour}:${minute}`
}

export function formatOptionLabel(
  opt: {
    kind: 'date' | 'datetime' | 'text'
    startAt: string | null
    endAt: string | null
    label: string | null
  },
  o: { locale: string; timeZone: string },
): { primary: string; secondary?: string; tertiary?: string } {
  const locale = localeFor(o.locale)

  if (opt.kind === 'text') {
    return { primary: opt.label ?? '' }
  }

  if (opt.kind === 'date') {
    const d = new Date(`${opt.startAt}T00:00:00Z`)
    return {
      primary: weekdayShort(d, locale, 'UTC'),
      secondary: dayMonth(d, locale, 'UTC'),
    }
  }

  // datetime
  const start = new Date(opt.startAt as string)
  const weekday = weekdayShort(start, locale, o.timeZone)
  const dm = dayMonth(start, locale, o.timeZone)
  const result: { primary: string; secondary?: string; tertiary?: string } = {
    primary: `${weekday} ${dm}`,
    secondary: hhmm(start, o.timeZone),
  }
  if (opt.endAt) {
    const end = new Date(opt.endAt)
    result.tertiary = `– ${hhmm(end, o.timeZone)}`
  }
  return result
}

export function isPast(iso: string, now: Date = new Date()): boolean {
  return new Date(iso).getTime() < now.getTime()
}
