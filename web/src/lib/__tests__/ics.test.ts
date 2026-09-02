import { describe, expect, it } from 'vitest'
import { buildIcs, buildIcsMulti } from '#/lib/ics'
describe('ics', () => {
  const now = new Date('2026-08-20T10:00:00.000Z')
  it('builds an all-day event', () => {
    const ics = buildIcs({
      uid: 'abc@whenweall',
      title: 'Team; offsite',
      url: 'https://whenweall.com/p/x',
      start: { date: '2026-09-01' },
      now,
    })
    expect(ics).toContain('BEGIN:VCALENDAR\r\n')
    expect(ics).toContain('DTSTART;VALUE=DATE:20260901\r\n')
    expect(ics).toContain('DTEND;VALUE=DATE:20260902\r\n')
    expect(ics).toContain('SUMMARY:Team\\; offsite\r\n')
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

describe('buildIcsMulti', () => {
  const now = new Date('2026-08-20T10:00:00.000Z')

  it('builds a single VCALENDAR with one VEVENT per input event, in order', () => {
    const ics = buildIcsMulti(
      [
        {
          uid: 'a@whenweall',
          title: 'Slot A',
          url: 'https://x/p/1',
          start: { date: '2026-09-01' },
        },
        {
          uid: 'b@whenweall',
          title: 'Slot B',
          url: 'https://x/p/1',
          start: { dateTime: '2026-09-02T16:30:00.000Z' },
        },
      ],
      { now },
    )

    expect(ics.match(/BEGIN:VCALENDAR/g)).toHaveLength(1)
    expect(ics.match(/END:VCALENDAR/g)).toHaveLength(1)
    expect(ics.match(/BEGIN:VEVENT/g)).toHaveLength(2)
    expect(ics.match(/END:VEVENT/g)).toHaveLength(2)
    expect(ics).toContain('UID:a@whenweall\r\n')
    expect(ics).toContain('UID:b@whenweall\r\n')
    expect(ics.indexOf('UID:a@whenweall')).toBeLessThan(ics.indexOf('UID:b@whenweall'))
    expect(ics.endsWith('END:VCALENDAR\r\n')).toBe(true)
  })

  it('returns an empty-body calendar for zero events', () => {
    const ics = buildIcsMulti([], { now })
    expect(ics).toContain('BEGIN:VCALENDAR\r\n')
    expect(ics).toContain('END:VCALENDAR\r\n')
    expect(ics).not.toContain('BEGIN:VEVENT')
  })
})
