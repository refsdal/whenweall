import { describe, expect, it } from 'vitest'
import { generateSlots, isSlotAvailable, type Interval, type PageRules } from '#/lib/availability'

const atNoonUtc = (dateStr: string) => new Date(`${dateStr}T12:00:00Z`)

/** Every slot must have exactly `durationMin`, end > start, and starts must be strictly increasing. */
function assertSlotInvariants(slots: Interval[], durationMin: number) {
  const durationMs = durationMin * 60_000
  for (const slot of slots) {
    const start = new Date(slot.start).getTime()
    const end = new Date(slot.end).getTime()
    expect(end).toBeGreaterThan(start)
    expect(end - start).toBe(durationMs)
  }
  for (let i = 1; i < slots.length; i++) {
    expect(new Date(slots[i]!.start).getTime()).toBeGreaterThan(
      new Date(slots[i - 1]!.start).getTime(),
    )
  }
}

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

  it('lays slots on a UTC grid when a range spans the Europe/Oslo spring-forward gap (2026-03-29)', () => {
    const rules = baseRules({
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      availability: { '0': [{ start: '01:00', end: '04:00' }] }, // Sunday 2026-03-29
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-03-29'),
      to: atNoonUtc('2026-03-29'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    assertSlotInvariants(slots, 30)
    expect(slots[0]?.start).toBe('2026-03-29T00:00:00.000Z') // 01:00 CET
    expect(slots.at(-1)?.end).toBe('2026-03-29T02:00:00.000Z') // 04:00 CEST
    expect(new Date(slots.at(-1)!.end).getTime()).toBeLessThanOrEqual(
      new Date('2026-03-29T02:00:00.000Z').getTime(),
    )
  })

  it('lays slots on a UTC grid when a range spans the Europe/Oslo fall-back gap (2026-10-25)', () => {
    const rules = baseRules({
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      availability: { '0': [{ start: '01:00', end: '04:00' }] }, // Sunday 2026-10-25
    })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-10-25'),
      to: atNoonUtc('2026-10-25'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    assertSlotInvariants(slots, 30)
    expect(slots[0]?.start).toBe('2026-10-24T23:00:00.000Z') // 01:00 CEST
    expect(new Date(slots.at(-1)!.end).getTime()).toBeLessThanOrEqual(
      new Date('2026-10-25T03:00:00.000Z').getTime(), // 04:00 CET
    )
  })

  it('never emits a malformed slot across a 10-day window spanning the spring-forward transition', () => {
    const allDays: Record<string, { start: string; end: string }[]> = {}
    for (let d = 0; d <= 6; d++) allDays[String(d)] = [{ start: '01:00', end: '04:00' }]
    const rules = baseRules({ timezone: 'Europe/Oslo', slotDurationMin: 30, availability: allDays })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-03-24'),
      to: atNoonUtc('2026-04-02'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots.length).toBeGreaterThan(0)
    assertSlotInvariants(slots, 30)
  })

  it('never emits a malformed slot across a 10-day window spanning the fall-back transition', () => {
    const allDays: Record<string, { start: string; end: string }[]> = {}
    for (let d = 0; d <= 6; d++) allDays[String(d)] = [{ start: '01:00', end: '04:00' }]
    const rules = baseRules({ timezone: 'Europe/Oslo', slotDurationMin: 30, availability: allDays })
    const slots = generateSlots(rules, {
      from: atNoonUtc('2026-10-20'),
      to: atNoonUtc('2026-10-29'),
      now: new Date('2026-01-01T00:00:00Z'),
      busy: [],
    })
    expect(slots.length).toBeGreaterThan(0)
    assertSlotInvariants(slots, 30)
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

  it('is true for the slot right after the Europe/Oslo spring-forward gap', () => {
    const dstRules = baseRules({
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      availability: { '0': [{ start: '01:00', end: '04:00' }] }, // Sunday 2026-03-29
    })
    // rangeStart 01:00 -> 2026-03-29T00:00:00.000Z; the 3rd 30-min slot starts at 01:00Z, i.e.
    // right at/after the local 02:00->03:00 jump.
    expect(
      isSlotAvailable(dstRules, '2026-03-29T01:00:00.000Z', {
        now: new Date('2026-01-01T00:00:00Z'),
        busy: [],
      }),
    ).toBe(true)
  })
})
