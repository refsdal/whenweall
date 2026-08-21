import { describe, expect, it } from 'vitest'
import {
  availabilitySchema,
  bookSlotSchema,
  createBookingPageSchema,
  dateOverridesSchema,
  handleSchema,
  manageBookingSchema,
  publicAvailabilityQuerySchema,
  rescheduleSchema,
  slugSchema,
  timeRangeSchema,
  updateBookingPageSchema,
} from '#/server/bookings/schemas'

describe('handleSchema / slugSchema', () => {
  it('accepts valid handles', () => {
    expect(handleSchema.safeParse('anders').success).toBe(true)
    expect(handleSchema.safeParse('a-b-c').success).toBe(true)
    expect(handleSchema.safeParse('a1b').success).toBe(true)
  })

  it('rejects too short, too long, uppercase, leading/trailing hyphen, and invalid chars', () => {
    expect(handleSchema.safeParse('ab').success).toBe(false)
    expect(handleSchema.safeParse('a'.repeat(31)).success).toBe(false)
    expect(handleSchema.safeParse('Anders').success).toBe(false)
    expect(handleSchema.safeParse('-abc').success).toBe(false)
    expect(handleSchema.safeParse('abc-').success).toBe(false)
    expect(handleSchema.safeParse('ab_c').success).toBe(false)
    expect(handleSchema.safeParse('ab c').success).toBe(false)
  })

  it('slugSchema follows the same rules', () => {
    expect(slugSchema.safeParse('intro-call').success).toBe(true)
    expect(slugSchema.safeParse('IntroCall').success).toBe(false)
  })
})

describe('timeRangeSchema', () => {
  it('accepts 15-minute-aligned HH:mm with start < end', () => {
    expect(timeRangeSchema.safeParse({ start: '09:00', end: '09:15' }).success).toBe(true)
    expect(timeRangeSchema.safeParse({ start: '23:45', end: '23:45' }).success).toBe(false)
  })

  it('rejects unaligned minutes', () => {
    expect(timeRangeSchema.safeParse({ start: '09:05', end: '09:30' }).success).toBe(false)
    expect(timeRangeSchema.safeParse({ start: '09:00', end: '09:31' }).success).toBe(false)
  })

  it('rejects end <= start', () => {
    expect(timeRangeSchema.safeParse({ start: '10:00', end: '09:00' }).success).toBe(false)
  })

  it('rejects malformed hour/minute', () => {
    expect(timeRangeSchema.safeParse({ start: '24:00', end: '25:00' }).success).toBe(false)
    expect(timeRangeSchema.safeParse({ start: '9:00', end: '10:00' }).success).toBe(false)
  })
})

describe('availabilitySchema', () => {
  it('accepts weekday keys 0..6 with sorted, non-overlapping ranges', () => {
    const result = availabilitySchema.safeParse({
      '0': [],
      '1': [
        { start: '09:00', end: '12:00' },
        { start: '13:00', end: '17:00' },
      ],
    })
    expect(result.success).toBe(true)
  })

  it('rejects keys outside 0..6', () => {
    expect(availabilitySchema.safeParse({ '7': [] }).success).toBe(false)
    expect(availabilitySchema.safeParse({ monday: [] }).success).toBe(false)
    expect(availabilitySchema.safeParse({ '-1': [] }).success).toBe(false)
  })

  it('rejects overlapping ranges for a weekday', () => {
    const result = availabilitySchema.safeParse({
      '1': [
        { start: '09:00', end: '12:00' },
        { start: '11:00', end: '13:00' },
      ],
    })
    expect(result.success).toBe(false)
  })

  it('rejects unsorted ranges for a weekday', () => {
    const result = availabilitySchema.safeParse({
      '1': [
        { start: '13:00', end: '17:00' },
        { start: '09:00', end: '12:00' },
      ],
    })
    expect(result.success).toBe(false)
  })

  it('rejects more than 20 ranges in a day', () => {
    const ranges = Array.from({ length: 21 }, (_, i) => ({
      start: `${String(i).padStart(2, '0')}:00`,
      end: `${String(i).padStart(2, '0')}:15`,
    }))
    expect(availabilitySchema.safeParse({ '1': ranges }).success).toBe(false)
  })
})

describe('dateOverridesSchema', () => {
  it('accepts YYYY-MM-DD keys, empty array meaning day off', () => {
    const result = dateOverridesSchema.safeParse({
      '2026-12-24': [],
      '2026-12-31': [{ start: '09:00', end: '10:00' }],
    })
    expect(result.success).toBe(true)
  })

  it('rejects malformed date keys', () => {
    expect(dateOverridesSchema.safeParse({ '2026-1-1': [] }).success).toBe(false)
    expect(dateOverridesSchema.safeParse({ 'not-a-date': [] }).success).toBe(false)
  })

  it('rejects more than 366 entries', () => {
    const overrides: Record<string, []> = {}
    for (let i = 0; i < 367; i++) {
      const d = new Date(Date.UTC(2027, 0, 1) + i * 86_400_000)
      overrides[d.toISOString().slice(0, 10)] = []
    }
    expect(dateOverridesSchema.safeParse(overrides).success).toBe(false)
  })
})

const validPage = {
  slug: 'intro-call',
  title: '15 min intro',
  timezone: 'Europe/Oslo',
  slotDurationMin: 30,
  bufferBeforeMin: 0,
  bufferAfterMin: 0,
  minNoticeMin: 120,
  maxDaysAhead: 60,
  availability: { '1': [{ start: '09:00', end: '17:00' }] },
  googleSync: false,
  reminders: true,
}

describe('createBookingPageSchema', () => {
  it('accepts a valid page', () => {
    expect(createBookingPageSchema.safeParse(validPage).success).toBe(true)
  })

  it('rejects an invalid timezone', () => {
    const result = createBookingPageSchema.safeParse({ ...validPage, timezone: 'Not/AZone' })
    expect(result.success).toBe(false)
  })

  it('rejects out-of-range duration and buffers', () => {
    expect(createBookingPageSchema.safeParse({ ...validPage, slotDurationMin: 10 }).success).toBe(
      false,
    )
    expect(createBookingPageSchema.safeParse({ ...validPage, slotDurationMin: 481 }).success).toBe(
      false,
    )
    expect(createBookingPageSchema.safeParse({ ...validPage, bufferBeforeMin: 121 }).success).toBe(
      false,
    )
  })
})

describe('updateBookingPageSchema', () => {
  it('accepts a partial update with pageId and status', () => {
    const result = updateBookingPageSchema.safeParse({
      pageId: 'abc123',
      status: 'paused',
      title: 'New title',
    })
    expect(result.success).toBe(true)
  })

  it('requires pageId', () => {
    expect(updateBookingPageSchema.safeParse({ title: 'x' }).success).toBe(false)
  })
})

describe('publicAvailabilityQuerySchema', () => {
  const base = {
    handle: 'anders',
    slug: 'intro-call',
    timezone: 'Europe/Oslo',
  }

  it('accepts a window of 62 days or less', () => {
    expect(
      publicAvailabilityQuerySchema.safeParse({ ...base, from: '2026-01-01', to: '2026-03-03' })
        .success,
    ).toBe(true)
  })

  it('rejects a window greater than 62 days', () => {
    expect(
      publicAvailabilityQuerySchema.safeParse({ ...base, from: '2026-01-01', to: '2026-03-10' })
        .success,
    ).toBe(false)
  })

  it('rejects to before from', () => {
    expect(
      publicAvailabilityQuerySchema.safeParse({ ...base, from: '2026-03-10', to: '2026-01-01' })
        .success,
    ).toBe(false)
  })
})

describe('bookSlotSchema', () => {
  const base = {
    pageId: 'abc123',
    startAt: '2026-01-05T09:00:00.000Z',
    name: 'Ada Lovelace',
    timezone: 'Europe/Oslo',
  }

  it('requires email', () => {
    expect(bookSlotSchema.safeParse(base).success).toBe(false)
    expect(bookSlotSchema.safeParse({ ...base, email: 'not-an-email' }).success).toBe(false)
    expect(bookSlotSchema.safeParse({ ...base, email: 'ada@example.com' }).success).toBe(true)
  })
})

describe('manageBookingSchema / rescheduleSchema', () => {
  it('token is optional for the owner path', () => {
    expect(manageBookingSchema.safeParse({ bookingId: 'b1' }).success).toBe(true)
    expect(manageBookingSchema.safeParse({ bookingId: 'b1', token: 't' }).success).toBe(true)
  })

  it('rescheduleSchema requires a valid startAt', () => {
    expect(
      rescheduleSchema.safeParse({ bookingId: 'b1', startAt: '2026-01-05T09:00:00.000Z' }).success,
    ).toBe(true)
    expect(rescheduleSchema.safeParse({ bookingId: 'b1', startAt: 'not-a-date' }).success).toBe(
      false,
    )
  })
})
