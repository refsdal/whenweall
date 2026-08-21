import { describe, expect, it } from 'vitest'
import { generateSlots, isSlotAvailable, type PageRules } from '#/lib/availability'

const atNoonUtc = (dateStr: string) => new Date(`${dateStr}T12:00:00Z`)

function baseRules(overrides: Partial<PageRules> = {}): PageRules {
  return {
    timezone: 'UTC',
    slotDurationMin: 30,
    bufferBeforeMin: 0,
    bufferAfterMin: 0,
    minNoticeMin: 0,
    maxDaysAhead: 365,
    availability: {},
    dateOverrides: null,
    ...overrides,
  }
}

describe('generateSlots', () => {
  it('produces expected UTC slots from a weekly range (Europe/Oslo Mon 09:00-11:00, 30 min, summer)', () => {
    // 2026-07-06 is a Monday, Oslo is CEST (UTC+2) in July.
    const rules = baseRules({
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      availability: { '1': [{ start: '09:00', end: '11:00' }] },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-07-06'),
      to: atNoonUtc('2026-07-06'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots.map((s) => s.start)).toEqual([
      '2026-07-06T07:00:00.000Z',
      '2026-07-06T07:30:00.000Z',
      '2026-07-06T08:00:00.000Z',
      '2026-07-06T08:30:00.000Z',
    ])
    expect(slots[0]?.end).toBe('2026-07-06T07:30:00.000Z')
  })

  it('date override with an empty array takes the day off, overriding weekly availability', () => {
    const rules = baseRules({
      availability: { '1': [{ start: '09:00', end: '10:00' }] }, // Monday
      dateOverrides: { '2026-01-05': [] }, // Monday 2026-01-05
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-05'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots).toEqual([])
  })

  it('date override adds extra hours on a day with no weekly availability', () => {
    const rules = baseRules({
      availability: {}, // nothing scheduled any weekday
      dateOverrides: { '2026-01-05': [{ start: '09:00', end: '09:30' }] },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-05'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots.map((s) => s.start)).toEqual(['2026-01-05T09:00:00.000Z'])
  })

  it('buffers exclude slots whose padded interval overlaps a busy interval in the gap between ranges', () => {
    const rules = baseRules({
      availability: {
        '1': [
          { start: '09:00', end: '09:30' },
          { start: '09:45', end: '10:15' },
        ],
      },
      bufferBeforeMin: 15,
      bufferAfterMin: 15,
    })
    const opts = {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-05'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [{ start: '2026-01-05T09:35:00.000Z', end: '2026-01-05T09:40:00.000Z' }],
    }
    expect(generateSlots(rules, opts)).toEqual([])
    expect(generateSlots({ ...rules, bufferBeforeMin: 0, bufferAfterMin: 0 }, opts)).toHaveLength(2)
  })

  it('excludes slots that start before now + minNoticeMin', () => {
    const rules = baseRules({
      minNoticeMin: 120,
      availability: {
        '1': [
          { start: '09:00', end: '09:30' },
          { start: '11:00', end: '11:30' },
        ],
      },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-05'),
      now: new Date('2026-01-05T08:00:00.000Z'),
      busy: [],
    })
    expect(slots.map((s) => s.start)).toEqual(['2026-01-05T11:00:00.000Z'])
  })

  it('excludes slots that end after now + maxDaysAhead', () => {
    const rules = baseRules({
      maxDaysAhead: 1,
      availability: {
        '1': [{ start: '09:00', end: '09:30' }], // Monday 2026-01-05
        '4': [{ start: '09:00', end: '09:30' }], // Thursday 2026-01-08
      },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-08'),
      now: new Date('2026-01-05T00:00:00.000Z'),
      busy: [],
    })
    expect(slots.map((s) => s.start)).toEqual(['2026-01-05T09:00:00.000Z'])
  })

  it('excludes a slot with only a partial overlap against a busy interval', () => {
    const rules = baseRules({
      availability: { '1': [{ start: '09:00', end: '09:30' }] },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-05'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [{ start: '2026-01-05T09:15:00.000Z', end: '2026-01-05T09:45:00.000Z' }],
    })
    expect(slots).toEqual([])
  })

  it('is DST-safe across the Europe/Oslo spring-forward transition (2026-03-29)', () => {
    const rules = baseRules({
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      availability: {
        '6': [{ start: '09:00', end: '09:30' }], // Saturday 2026-03-28, still CET (+1)
        '1': [{ start: '09:00', end: '09:30' }], // Monday 2026-03-30, now CEST (+2)
      },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-03-28'),
      to: atNoonUtc('2026-03-30'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots).toEqual([
      { start: '2026-03-28T08:00:00.000Z', end: '2026-03-28T08:30:00.000Z' },
      { start: '2026-03-30T07:00:00.000Z', end: '2026-03-30T07:30:00.000Z' },
    ])
  })

  it('is DST-safe across the Europe/Oslo fall-back transition (2026-10-25)', () => {
    const rules = baseRules({
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      availability: {
        '6': [{ start: '09:00', end: '09:30' }], // Saturday 2026-10-24, still CEST (+2)
        '1': [{ start: '09:00', end: '09:30' }], // Monday 2026-10-26, now CET (+1)
      },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-10-24'),
      to: atNoonUtc('2026-10-26'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots).toEqual([
      { start: '2026-10-24T07:00:00.000Z', end: '2026-10-24T07:30:00.000Z' },
      { start: '2026-10-26T08:00:00.000Z', end: '2026-10-26T08:30:00.000Z' },
    ])
  })

  it('supports a 15-minute duration', () => {
    const rules = baseRules({
      slotDurationMin: 15,
      availability: { '1': [{ start: '09:00', end: '10:00' }] },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-05'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots.map((s) => s.start)).toEqual([
      '2026-01-05T09:00:00.000Z',
      '2026-01-05T09:15:00.000Z',
      '2026-01-05T09:30:00.000Z',
      '2026-01-05T09:45:00.000Z',
    ])
  })

  it('supports a 60-minute duration', () => {
    const rules = baseRules({
      slotDurationMin: 60,
      availability: { '1': [{ start: '09:00', end: '10:00' }] },
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-05'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots.map((s) => s.start)).toEqual(['2026-01-05T09:00:00.000Z'])
  })

  it('returns [] for empty availability', () => {
    const rules = baseRules({ availability: {} })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-01-05'),
      to: atNoonUtc('2026-01-11'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots).toEqual([])
  })
})

describe('isSlotAvailable', () => {
  const rules = baseRules({
    availability: { '1': [{ start: '09:00', end: '10:00' }] },
  })

  it('is true for a slot generateSlots would produce', () => {
    expect(
      isSlotAvailable(rules, '2026-01-05T09:00:00.000Z', {
        now: new Date('2026-01-01T00:00:00Z'),
        busy: [],
      }),
    ).toBe(true)
  })

  it('is false for a time that is not on the slot grid or outside availability', () => {
    expect(
      isSlotAvailable(rules, '2026-01-05T09:05:00.000Z', {
        now: new Date('2026-01-01T00:00:00Z'),
        busy: [],
      }),
    ).toBe(false)
    expect(
      isSlotAvailable(rules, '2026-01-05T12:00:00.000Z', {
        now: new Date('2026-01-01T00:00:00Z'),
        busy: [],
      }),
    ).toBe(false)
  })
})
