import { describe, expect, it } from 'vitest'
import { buildIcs } from '#/lib/ics'
describe('ics', () => {
  const now = new Date('2026-08-20T10:00:00.000Z')
  it('builds an all-day event', () => {
    const ics = buildIcs({
      uid: 'abc@samla',
      title: 'Team; offsite',
      url: 'https://samla.app/p/x',
      start: { date: '2026-09-01' },
      now,
    })
    expect(ics).toContain('BEGIN:VCALENDAR\r\n')
    expect(ics).toContain('DTSTART;VALUE=DATE:20260901\r\n')
    expect(ics).toContain('DTEND;VALUE=DATE:20260902\r\n')
    // eslint-disable-next-line no-useless-escape -- verbatim from task brief; \; collapses to ; per JS string escaping
    expect(ics).toContain('SUMMARY:Team\; offsite\r\n')
    expect(ics).toContain('DTSTAMP:20260820T100000Z\r\n')
    expect(ics.endsWith('END:VCALENDAR\r\n')).toBe(true)
  })
  it('builds a timed event with 1h default duration and escapes newlines', () => {
    const ics = buildIcs({
      uid: 'u',
      title: 'Call',
      description: 'line1\nline2',
      url: 'https://x',
      start: { dateTime: '2026-09-01T16:30:00.000Z' },
      now,
    })
    expect(ics).toContain('DTSTART:20260901T163000Z\r\n')
    expect(ics).toContain('DTEND:20260901T173000Z\r\n')
    expect(ics).toContain('DESCRIPTION:line1\\nline2\r\n')
  })
})
