import { describe, expect, it } from 'vitest'
import {
  addCommentSchema,
  addParticipantSchema,
  createPollSchema,
  updatePollSchema,
} from '#/server/polls/schemas'

const VALID_POLL_ID = 'abc123XYZ012'

function datetimeOption(startAt: string, endAt?: string) {
  return endAt
    ? { kind: 'datetime' as const, startAt, endAt }
    : { kind: 'datetime' as const, startAt }
}

function baseDatetimePoll(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    type: 'datetime' as const,
    title: 'Team sync',
    timezone: 'Europe/Oslo',
    options: [
      datetimeOption('2026-09-01T10:00:00.000Z', '2026-09-01T11:00:00.000Z'),
      datetimeOption('2026-09-02T10:00:00.000Z'),
    ],
    ...overrides,
  }
}

describe('createPollSchema', () => {
  it('accepts a valid datetime poll', () => {
    const result = createPollSchema.safeParse(baseDatetimePoll())
    expect(result.success).toBe(true)
  })

  it('rejects an options poll containing a date option', () => {
    const result = createPollSchema.safeParse({
      type: 'options',
      title: 'Pick a date',
      timezone: 'Europe/Oslo',
      options: [{ kind: 'date', date: '2026-09-01' }],
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.path).toEqual(['options', 0])
    }
  })

  it('rejects a datetime poll containing a text option', () => {
    const result = createPollSchema.safeParse(
      baseDatetimePoll({
        options: [datetimeOption('2026-09-01T10:00:00.000Z'), { kind: 'text', label: 'Pizza' }],
      }),
    )
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.path).toEqual(['options', 1])
    }
  })

  it('rejects a title longer than 200 characters', () => {
    const result = createPollSchema.safeParse(baseDatetimePoll({ title: 'x'.repeat(201) }))
    expect(result.success).toBe(false)
  })

  it('rejects more than 100 options', () => {
    const options = Array.from({ length: 101 }, (_, i) =>
      datetimeOption(new Date(2026, 0, i + 1).toISOString()),
    )
    const result = createPollSchema.safeParse(baseDatetimePoll({ options }))
    expect(result.success).toBe(false)
  })

  it('rejects a datetime option whose endAt is not after startAt', () => {
    const result = createPollSchema.safeParse(
      baseDatetimePoll({
        options: [datetimeOption('2026-09-01T10:00:00.000Z', '2026-09-01T10:00:00.000Z')],
      }),
    )
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.path).toEqual(['options', 0, 'endAt'])
    }
  })

  it('rejects duplicate date options', () => {
    const result = createPollSchema.safeParse({
      type: 'datetime',
      title: 'Dupes',
      timezone: 'Europe/Oslo',
      options: [
        { kind: 'date', date: '2026-09-01' },
        { kind: 'date', date: '2026-09-01' },
      ],
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.path).toEqual(['options', 1])
    }
  })

  it('rejects duplicate datetime options (same startAt|endAt)', () => {
    const result = createPollSchema.safeParse(
      baseDatetimePoll({
        options: [
          datetimeOption('2026-09-01T10:00:00.000Z', '2026-09-01T11:00:00.000Z'),
          datetimeOption('2026-09-01T10:00:00.000Z', '2026-09-01T11:00:00.000Z'),
        ],
      }),
    )
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.path).toEqual(['options', 1])
    }
  })

  it('rejects duplicate text option labels after trim/lowercase', () => {
    const result = createPollSchema.safeParse({
      type: 'options',
      title: 'Dupes',
      timezone: 'Europe/Oslo',
      options: [
        { kind: 'text', label: 'Pizza' },
        { kind: 'text', label: '  pizza  ' },
      ],
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.path).toEqual(['options', 1])
    }
  })
})

describe('updatePollSchema', () => {
  it('requires pollId', () => {
    const result = updatePollSchema.safeParse({ title: 'New title' })
    expect(result.success).toBe(false)
  })

  it('accepts a partial body with just pollId and one field', () => {
    const result = updatePollSchema.safeParse({ pollId: VALID_POLL_ID, title: 'New title' })
    expect(result.success).toBe(true)
  })

  it('accepts pollId alone with no other fields', () => {
    const result = updatePollSchema.safeParse({ pollId: VALID_POLL_ID })
    expect(result.success).toBe(true)
  })

  it('still enforces endAt > startAt on options provided in an update', () => {
    const result = updatePollSchema.safeParse({
      pollId: VALID_POLL_ID,
      options: [datetimeOption('2026-09-01T10:00:00.000Z', '2026-09-01T09:00:00.000Z')],
    })
    expect(result.success).toBe(false)
    if (!result.success) {
      expect(result.error.issues[0]?.path).toEqual(['options', 0, 'endAt'])
    }
  })

  it('does not include type in the schema (updates cannot change poll type)', () => {
    const result = updatePollSchema.safeParse({
      pollId: VALID_POLL_ID,
      type: 'options',
    })
    expect(result.success).toBe(true)
    if (result.success) {
      expect(result.data).not.toHaveProperty('type')
    }
  })
})

describe('addParticipantSchema', () => {
  const base = {
    pollId: VALID_POLL_ID,
    name: 'Ada',
    answers: { opt1: 'yes' as const },
  }

  it('accepts an empty string email', () => {
    const result = addParticipantSchema.safeParse({ ...base, email: '' })
    expect(result.success).toBe(true)
  })

  it('rejects an invalid email', () => {
    const result = addParticipantSchema.safeParse({ ...base, email: 'nope' })
    expect(result.success).toBe(false)
  })

  it('rejects an unknown answer value', () => {
    const result = addParticipantSchema.safeParse({
      ...base,
      answers: { opt1: 'maybe' },
    })
    expect(result.success).toBe(false)
  })
})

describe('addCommentSchema', () => {
  it('accepts a valid comment', () => {
    const result = addCommentSchema.safeParse({
      pollId: VALID_POLL_ID,
      authorName: 'Ada',
      body: 'Looks good!',
    })
    expect(result.success).toBe(true)
  })
})
