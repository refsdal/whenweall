function pad(n: number, width = 2): string {
  return String(n).padStart(width, '0')
}

function formatUtcBasic(d: Date): string {
  return (
    `${d.getUTCFullYear()}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}` +
    `T${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}${pad(d.getUTCSeconds())}Z`
  )
}

function formatDateBasic(dateStr: string): string {
  return dateStr.replace(/-/g, '')
}

function nextDayBasic(dateStr: string): string {
  const [y, m, d] = dateStr.split('-').map(Number)
  const next = new Date(Date.UTC(y, m - 1, d + 1))
  return `${next.getUTCFullYear()}${pad(next.getUTCMonth() + 1)}${pad(next.getUTCDate())}`
}

function escapeText(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/;/g, '\\;')
    .replace(/,/g, '\\,')
    .replace(/\n/g, '\\n')
}

function byteLength(str: string): number {
  return new TextEncoder().encode(str).length
}

function foldLine(line: string): string {
  const LIMIT = 75
  if (byteLength(line) <= LIMIT) return line

  const chunks: string[] = []
  let current = ''
  let currentBytes = 0

  for (const char of line) {
    const charBytes = byteLength(char)
    const isContinuation = chunks.length > 0
    const limit = isContinuation ? LIMIT - 1 : LIMIT
    if (currentBytes + charBytes > limit) {
      chunks.push(current)
      current = ''
      currentBytes = 0
    }
    current += char
    currentBytes += charBytes
  }
  if (current) chunks.push(current)

  return chunks.join('\r\n ')
}

export type IcsEvent = {
  uid: string
  title: string
  description?: string | null
  location?: string | null
  url: string
  start: { date: string } | { dateTime: string; endDateTime?: string | null }
}

function buildVevent(e: IcsEvent, now: Date): string[] {
  let dtstart: string
  let dtend: string
  if ('date' in e.start) {
    dtstart = `DTSTART;VALUE=DATE:${formatDateBasic(e.start.date)}`
    dtend = `DTEND;VALUE=DATE:${nextDayBasic(e.start.date)}`
  } else {
    const start = new Date(e.start.dateTime)
    const end = e.start.endDateTime
      ? new Date(e.start.endDateTime)
      : new Date(start.getTime() + 60 * 60 * 1000)
    dtstart = `DTSTART:${formatUtcBasic(start)}`
    dtend = `DTEND:${formatUtcBasic(end)}`
  }

  const lines: string[] = [
    'BEGIN:VEVENT',
    `UID:${escapeText(e.uid)}`,
    `DTSTAMP:${formatUtcBasic(now)}`,
    dtstart,
    dtend,
    `SUMMARY:${escapeText(e.title)}`,
  ]

  if (e.description) lines.push(`DESCRIPTION:${escapeText(e.description)}`)
  if (e.location) lines.push(`LOCATION:${escapeText(e.location)}`)
  lines.push(`URL:${escapeText(e.url)}`)
  lines.push('END:VEVENT')

  return lines
}

export function buildIcs(e: IcsEvent & { now?: Date }): string {
  const now = e.now ?? new Date()
  const lines: string[] = [
    'BEGIN:VCALENDAR',
    'VERSION:2.0',
    'PRODID:-//samla//EN',
    'CALSCALE:GREGORIAN',
    'METHOD:PUBLISH',
    ...buildVevent(e, now),
    'END:VCALENDAR',
  ]

  return lines.map(foldLine).join('\r\n') + '\r\n'
}

/**
 * Same wire format as `buildIcs`, but with one VEVENT per input event inside a single VCALENDAR —
 * used for the sign-up claim confirmation email, where a participant may hold several slots at
 * once and a mail client should be able to add all of them from one attachment.
 */
export function buildIcsMulti(events: IcsEvent[], opts: { now?: Date } = {}): string {
  const now = opts.now ?? new Date()
  const lines: string[] = [
    'BEGIN:VCALENDAR',
    'VERSION:2.0',
    'PRODID:-//samla//EN',
    'CALSCALE:GREGORIAN',
    'METHOD:PUBLISH',
    ...events.flatMap((e) => buildVevent(e, now)),
    'END:VCALENDAR',
  ]

  return lines.map(foldLine).join('\r\n') + '\r\n'
}
