import { describe, expect, it } from 'vitest'
import { formatOptionLabel, isPast, localToUtcIso, utcIsoToLocalParts } from '#/lib/time'
describe('time', () => {
  it('converts Oslo local time to UTC across DST', () => {
    expect(localToUtcIso('2026-07-01', '18:30', 'Europe/Oslo')).toBe('2026-07-01T16:30:00.000Z')
    expect(localToUtcIso('2026-12-01', '18:30', 'Europe/Oslo')).toBe('2026-12-01T17:30:00.000Z')
  })
  it('round-trips', () => {
    expect(utcIsoToLocalParts('2026-07-01T16:30:00.000Z', 'Europe/Oslo')).toEqual({
      date: '2026-07-01',
      time: '18:30',
    })
    expect(utcIsoToLocalParts('2026-07-01T16:30:00.000Z', 'America/New_York')).toEqual({
      date: '2026-07-01',
      time: '12:30',
    })
  })
  it('formats labels per locale and zone', () => {
    expect(
      formatOptionLabel(
        { kind: 'date', startAt: '2026-09-01', endAt: null, label: null },
        { locale: 'en', timeZone: 'Europe/Oslo' },
      ),
    ).toEqual({ primary: 'Tue', secondary: '1 Sep' })
    const dt = formatOptionLabel(
      {
        kind: 'datetime',
        startAt: '2026-09-01T16:30:00.000Z',
        endAt: '2026-09-01T17:30:00.000Z',
        label: null,
      },
      { locale: 'en', timeZone: 'Europe/Oslo' },
    )
    expect(dt.primary).toBe('Tue 1 Sep')
    expect(dt.secondary).toBe('18:30')
    expect(dt.tertiary).toBe('– 19:30')
    expect(
      formatOptionLabel(
        { kind: 'date', startAt: '2026-09-01', endAt: null, label: null },
        { locale: 'nb', timeZone: 'Europe/Oslo' },
      ).primary.toLowerCase(),
    ).toMatch(/^tir/)
    expect(
      formatOptionLabel(
        { kind: 'text', startAt: null, endAt: null, label: 'Pizza' },
        { locale: 'en', timeZone: 'UTC' },
      ),
    ).toEqual({ primary: 'Pizza' })
  })
  it('isPast', () => {
    expect(isPast('2000-01-01T00:00:00.000Z')).toBe(true)
    expect(isPast('2999-01-01T00:00:00.000Z')).toBe(false)
  })
})
