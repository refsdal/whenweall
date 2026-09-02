import { describe, expect, it } from 'vitest'
import {
  canAdvance,
  countOptions,
  creatorReducer,
  draftFromPoll,
  draftToInput,
  initialDraft,
  type CreatorDraft,
} from '#/components/creator/creator-state'
import type { PollView } from '#/api/types'

const OSLO = 'Europe/Oslo'

function draft(overrides: Partial<CreatorDraft> = {}): CreatorDraft {
  return { ...initialDraft(OSLO), ...overrides }
}

describe('initialDraft', () => {
  it('starts on step 0 as a datetime poll in the given timezone', () => {
    const d = initialDraft(OSLO)

    expect(d.step).toBe(0)
    expect(d.type).toBe('datetime')
    expect(d.timezone).toBe(OSLO)
    expect(d.dates).toEqual([])
    expect(d.deadlineAt).toBeNull()
  })

  it('defaults to the friendly settings: if-need-be and comments on, email off', () => {
    const d = initialDraft(OSLO)

    expect(d.allowIfNeedBe).toBe(true)
    expect(d.allowComments).toBe(true)
    expect(d.requireParticipantEmail).toBe(false)
  })
})

describe('creatorReducer / setField', () => {
  it('sets a single field and leaves the rest alone', () => {
    const d = creatorReducer(draft(), { type: 'setField', field: 'title', value: 'Team lunch' })

    expect(d.title).toBe('Team lunch')
    expect(d.description).toBe('')
  })
})

describe('creatorReducer / toggleDate', () => {
  it('adds a date with no slots', () => {
    const d = creatorReducer(draft(), { type: 'toggleDate', date: '2026-06-15' })

    expect(d.dates).toEqual([{ date: '2026-06-15', slots: [] }])
  })

  it('removes a date that is already selected', () => {
    const withDate = creatorReducer(draft(), { type: 'toggleDate', date: '2026-06-15' })
    const d = creatorReducer(withDate, { type: 'toggleDate', date: '2026-06-15' })

    expect(d.dates).toEqual([])
  })

  it('keeps the selected dates sorted ascending', () => {
    let d = draft()
    for (const date of ['2026-06-20', '2026-06-15', '2026-06-17']) {
      d = creatorReducer(d, { type: 'toggleDate', date })
    }

    expect(d.dates.map((x) => x.date)).toEqual(['2026-06-15', '2026-06-17', '2026-06-20'])
  })
})

describe('creatorReducer / slots', () => {
  const base = creatorReducer(draft(), { type: 'toggleDate', date: '2026-06-15' })

  it('adds a slot to the matching date only', () => {
    const other = creatorReducer(base, { type: 'toggleDate', date: '2026-06-16' })
    const d = creatorReducer(other, {
      type: 'addSlot',
      date: '2026-06-15',
      start: '09:00',
      end: '10:00',
    })

    expect(d.dates[0]?.slots).toEqual([{ start: '09:00', end: '10:00' }])
    expect(d.dates[1]?.slots).toEqual([])
  })

  it('accepts a slot without an end time', () => {
    const d = creatorReducer(base, {
      type: 'addSlot',
      date: '2026-06-15',
      start: '18:00',
      end: null,
    })

    expect(d.dates[0]?.slots).toEqual([{ start: '18:00', end: null }])
  })

  it('keeps slots sorted by start time and ignores an exact duplicate', () => {
    let d = creatorReducer(base, { type: 'addSlot', date: '2026-06-15', start: '14:00', end: null })
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '09:00', end: '10:00' })
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '14:00', end: null })

    expect(d.dates[0]?.slots).toEqual([
      { start: '09:00', end: '10:00' },
      { start: '14:00', end: null },
    ])
  })

  it('removes a slot by index', () => {
    let d = creatorReducer(base, { type: 'addSlot', date: '2026-06-15', start: '09:00', end: null })
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '14:00', end: null })
    d = creatorReducer(d, { type: 'removeSlot', date: '2026-06-15', index: 0 })

    expect(d.dates[0]?.slots).toEqual([{ start: '14:00', end: null }])
  })

  it('ignores a slot addition for a date that is not selected', () => {
    const d = creatorReducer(base, {
      type: 'addSlot',
      date: '2026-07-01',
      start: '09:00',
      end: null,
    })

    expect(d).toEqual(base)
  })
})

describe('creatorReducer / applySlotsToAll', () => {
  it('copies one day slots onto every other selected day', () => {
    let d = draft()
    for (const date of ['2026-06-15', '2026-06-16', '2026-06-17']) {
      d = creatorReducer(d, { type: 'toggleDate', date })
    }
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-16', start: '09:00', end: '10:00' })
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-16', start: '13:00', end: null })
    d = creatorReducer(d, { type: 'applySlotsToAll', fromDate: '2026-06-16' })

    const expected = [
      { start: '09:00', end: '10:00' },
      { start: '13:00', end: null },
    ]
    expect(d.dates.map((x) => x.slots)).toEqual([expected, expected, expected])
  })

  it('copies the slots rather than sharing the same array', () => {
    let d = draft()
    for (const date of ['2026-06-15', '2026-06-16']) {
      d = creatorReducer(d, { type: 'toggleDate', date })
    }
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '09:00', end: null })
    d = creatorReducer(d, { type: 'applySlotsToAll', fromDate: '2026-06-15' })
    d = creatorReducer(d, { type: 'removeSlot', date: '2026-06-15', index: 0 })

    expect(d.dates[0]?.slots).toEqual([])
    expect(d.dates[1]?.slots).toEqual([{ start: '09:00', end: null }])
  })
})

describe('creatorReducer / signup capacity actions', () => {
  it('sets a slot capacity by index without touching other slots', () => {
    let d = creatorReducer(draft({ type: 'signup' }), { type: 'toggleDate', date: '2026-06-15' })
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '09:00', end: null })
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '13:00', end: null })

    d = creatorReducer(d, { type: 'setSlotCapacity', date: '2026-06-15', index: 1, capacity: 5 })

    expect(d.dates[0]?.slots).toEqual([
      { start: '09:00', end: null },
      { start: '13:00', end: null, capacity: 5 },
    ])
  })

  it('sets a slot capacity to unlimited (null)', () => {
    let d = creatorReducer(draft({ type: 'signup' }), { type: 'toggleDate', date: '2026-06-15' })
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '09:00', end: null })

    d = creatorReducer(d, { type: 'setSlotCapacity', date: '2026-06-15', index: 0, capacity: null })

    expect(d.dates[0]?.slots).toEqual([{ start: '09:00', end: null, capacity: null }])
  })

  it('sets an all-day date capacity', () => {
    let d = creatorReducer(draft({ type: 'signup' }), { type: 'toggleDate', date: '2026-06-15' })

    d = creatorReducer(d, { type: 'setDateCapacity', date: '2026-06-15', capacity: 3 })

    expect(d.dates[0]).toEqual({ date: '2026-06-15', slots: [], capacity: 3 })
  })

  it('sets a text option capacity by index', () => {
    const d = creatorReducer(
      draft({ type: 'signup', textOptions: [{ label: 'Setup' }, { label: 'Cleanup' }] }),
      {
        type: 'setTextOptionCapacity',
        index: 0,
        capacity: 2,
      },
    )

    expect(d.textOptions).toEqual([{ label: 'Setup', capacity: 2 }, { label: 'Cleanup' }])
  })
})

describe('creatorReducer / applySlotsToAll (signup capacity)', () => {
  it('copies slot capacity to every other day, but not ids', () => {
    let d = draft({ type: 'signup' })
    for (const date of ['2026-06-15', '2026-06-16']) {
      d = creatorReducer(d, { type: 'toggleDate', date })
    }
    d = creatorReducer(d, { type: 'addSlot', date: '2026-06-15', start: '09:00', end: null })
    d = creatorReducer(d, {
      type: 'setSlotCapacity',
      date: '2026-06-15',
      index: 0,
      capacity: 4,
    })

    d = creatorReducer(d, { type: 'applySlotsToAll', fromDate: '2026-06-15' })

    expect(d.dates[1]?.slots).toEqual([{ start: '09:00', end: null, capacity: 4 }])
  })
})

describe('creatorReducer / setTextOptions', () => {
  it('replaces the option list', () => {
    const d = creatorReducer(draft({ type: 'options' }), {
      type: 'setTextOptions',
      options: [{ label: 'Pizza' }, { label: 'Sushi' }],
    })

    expect(d.textOptions).toEqual([{ label: 'Pizza' }, { label: 'Sushi' }])
  })
})

describe('creatorReducer / next and back', () => {
  it('advances when the current step is complete', () => {
    const d = creatorReducer(draft({ title: 'Lunch' }), { type: 'next' })

    expect(d.step).toBe(1)
  })

  it('refuses to advance while the current step is incomplete', () => {
    const d = creatorReducer(draft({ title: '   ' }), { type: 'next' })

    expect(d.step).toBe(0)
  })

  it('never goes past the last step', () => {
    const d = creatorReducer(draft({ step: 2, title: 'Lunch' }), { type: 'next' })

    expect(d.step).toBe(2)
  })

  it('goes back and never before the first step', () => {
    expect(creatorReducer(draft({ step: 1 }), { type: 'back' }).step).toBe(0)
    expect(creatorReducer(draft({ step: 0 }), { type: 'back' }).step).toBe(0)
  })
})

describe('canAdvance', () => {
  it('requires a non-blank title on step 0', () => {
    expect(canAdvance(draft({ step: 0, title: '' }))).toBe(false)
    expect(canAdvance(draft({ step: 0, title: '   ' }))).toBe(false)
    expect(canAdvance(draft({ step: 0, title: 'Lunch' }))).toBe(true)
  })

  it('requires at least one option on step 1', () => {
    expect(canAdvance(draft({ step: 1 }))).toBe(false)
    expect(canAdvance(draft({ step: 1, dates: [{ date: '2026-06-15', slots: [] }] }))).toBe(true)
    expect(canAdvance(draft({ step: 1, type: 'options', textOptions: [{ label: '  ' }] }))).toBe(
      false,
    )
    expect(canAdvance(draft({ step: 1, type: 'options', textOptions: [{ label: 'Pizza' }] }))).toBe(
      true,
    )
  })

  it('rejects more options than the server allows', () => {
    const many = Array.from({ length: 101 }, (_, i) => ({ label: `Option ${i}` }))

    expect(canAdvance(draft({ step: 1, type: 'options', textOptions: many }))).toBe(false)
  })

  it('is always true on the final step', () => {
    expect(canAdvance(draft({ step: 2 }))).toBe(true)
  })
})

describe('countOptions', () => {
  it('counts an all-day date as one option and each slot as one', () => {
    const d = draft({
      dates: [
        { date: '2026-06-15', slots: [] },
        {
          date: '2026-06-16',
          slots: [
            { start: '09:00', end: null },
            { start: '13:00', end: null },
          ],
        },
      ],
    })

    expect(countOptions(d)).toBe(3)
  })

  it('counts only non-blank text options', () => {
    expect(
      countOptions(
        draft({
          type: 'options',
          textOptions: [{ label: 'Pizza' }, { label: '  ' }, { label: 'Sushi' }],
        }),
      ),
    ).toBe(2)
  })
})

describe('draftToInput', () => {
  it('produces date-kind options for days with no slots, sorted ascending', () => {
    const input = draftToInput(
      draft({
        title: 'Team lunch',
        dates: [
          { date: '2026-06-17', slots: [] },
          { date: '2026-06-15', slots: [] },
        ],
      }),
    )

    expect(input.type).toBe('datetime')
    expect(input.options).toEqual([
      { kind: 'date', date: '2026-06-15' },
      { kind: 'date', date: '2026-06-17' },
    ])
  })

  it('converts each slot from the organiser timezone to a UTC instant', () => {
    const input = draftToInput(
      draft({
        title: 'Team lunch',
        dates: [
          {
            date: '2026-06-15',
            slots: [
              { start: '09:00', end: '10:30' },
              { start: '18:00', end: null },
            ],
          },
        ],
      }),
    )

    // Oslo is UTC+2 in June (CEST).
    expect(input.options).toEqual([
      { kind: 'datetime', startAt: '2026-06-15T07:00:00.000Z', endAt: '2026-06-15T08:30:00.000Z' },
      { kind: 'datetime', startAt: '2026-06-15T16:00:00.000Z', endAt: null },
    ])
  })

  it('uses the winter offset for a winter date', () => {
    const input = draftToInput(
      draft({
        title: 'Team lunch',
        dates: [{ date: '2026-01-15', slots: [{ start: '09:00', end: null }] }],
      }),
    )

    // Oslo is UTC+1 in January (CET).
    expect(input.options).toEqual([
      { kind: 'datetime', startAt: '2026-01-15T08:00:00.000Z', endAt: null },
    ])
  })

  it('rolls an end time that lands before the start over to the next day', () => {
    const input = draftToInput(
      draft({
        title: 'Party',
        dates: [{ date: '2026-06-15', slots: [{ start: '22:00', end: '01:00' }] }],
      }),
    )

    expect(input.options).toEqual([
      { kind: 'datetime', startAt: '2026-06-15T20:00:00.000Z', endAt: '2026-06-15T23:00:00.000Z' },
    ])
  })

  it('trims text options and drops the blank ones', () => {
    const input = draftToInput(
      draft({
        type: 'options',
        title: 'Dinner',
        textOptions: [{ label: '  Pizza ' }, { label: '' }, { label: '   ' }, { label: 'Sushi' }],
      }),
    )

    expect(input.type).toBe('options')
    expect(input.options).toEqual([
      { kind: 'text', label: 'Pizza' },
      { kind: 'text', label: 'Sushi' },
    ])
  })

  it('carries an existing option id through so its votes are preserved', () => {
    const input = draftToInput(
      draft({
        type: 'options',
        title: 'Dinner',
        textOptions: [{ id: 'opt-1', label: 'Pizza' }, { label: 'Sushi' }],
      }),
    )

    expect(input.options).toEqual([
      { kind: 'text', label: 'Pizza', id: 'opt-1' },
      { kind: 'text', label: 'Sushi' },
    ])
  })

  it('trims the title and drops an empty description and location', () => {
    const input = draftToInput(
      draft({
        title: '  Team lunch  ',
        description: '   ',
        location: '  ',
        dates: [{ date: '2026-06-15', slots: [] }],
      }),
    )

    expect(input.title).toBe('Team lunch')
    expect(input.description).toBeUndefined()
    expect(input.location).toBeUndefined()
  })

  it('keeps a trimmed description and location when they have content', () => {
    const input = draftToInput(
      draft({
        title: 'Team lunch',
        description: '  Bring cash  ',
        location: '  Kaffebrenneriet  ',
        dates: [{ date: '2026-06-15', slots: [] }],
      }),
    )

    expect(input.description).toBe('Bring cash')
    expect(input.location).toBe('Kaffebrenneriet')
  })

  it('carries the timezone, deadline and settings through', () => {
    const input = draftToInput(
      draft({
        title: 'Team lunch',
        dates: [{ date: '2026-06-15', slots: [] }],
        deadlineAt: '2026-06-14T10:00:00.000Z',
        allowIfNeedBe: false,
        allowComments: false,
        requireParticipantEmail: true,
      }),
    )

    expect(input.timezone).toBe(OSLO)
    expect(input.deadlineAt).toBe('2026-06-14T10:00:00.000Z')
    expect(input.allowIfNeedBe).toBe(false)
    expect(input.allowComments).toBe(false)
    expect(input.requireParticipantEmail).toBe(true)
  })

  it('omits capacity and signupMaxClaims for non-signup polls', () => {
    const input = draftToInput(
      draft({
        title: 'Team lunch',
        dates: [{ date: '2026-06-15', slots: [{ start: '09:00', end: null, capacity: 5 }] }],
      }),
    )

    expect(input.options[0]).not.toHaveProperty('capacity')
    expect(input).not.toHaveProperty('signupMaxClaims')
  })

  it('emits capacity on every option for a signup poll, defaulting an unset slot to 1', () => {
    const input = draftToInput(
      draft({
        type: 'signup',
        title: 'Bake sale',
        signupMaxClaims: 2,
        dates: [
          {
            date: '2026-06-15',
            slots: [
              { start: '09:00', end: null, capacity: 3 },
              { start: '13:00', end: null },
              { start: '18:00', end: null, capacity: null },
            ],
          },
        ],
      }),
    )

    expect(input.options.map((o) => o.capacity)).toEqual([3, 1, null])
    expect(input.signupMaxClaims).toBe(2)
  })

  it('sets allowIfNeedBe to false for a signup poll regardless of the draft value', () => {
    const input = draftToInput(
      draft({
        type: 'signup',
        title: 'Bake sale',
        allowIfNeedBe: true,
        dates: [{ date: '2026-06-15', slots: [] }],
      }),
    )

    expect(input.allowIfNeedBe).toBe(false)
  })

  it('emits capacity for a signup poll with text options, defaulting unset ones to 1', () => {
    const input = draftToInput(
      draft({
        type: 'signup',
        title: 'Potluck',
        textOptions: [
          { label: 'Starter', capacity: 1 },
          { label: 'Main', capacity: null },
          { label: 'Dessert' },
        ],
      }),
    )

    expect(input.type).toBe('signup')
    expect(input.options).toEqual([
      { kind: 'text', label: 'Starter', capacity: 1 },
      { kind: 'text', label: 'Main', capacity: null },
      { kind: 'text', label: 'Dessert', capacity: 1 },
    ])
  })

  it('sets an all-day signup date option capacity, defaulting to 1 when unset', () => {
    const input = draftToInput(
      draft({
        type: 'signup',
        title: 'Volunteer day',
        dates: [
          { date: '2026-06-15', slots: [], capacity: 5 },
          { date: '2026-06-16', slots: [] },
        ],
      }),
    )

    expect(input.options).toEqual([
      { kind: 'date', date: '2026-06-15', capacity: 5 },
      { kind: 'date', date: '2026-06-16', capacity: 1 },
    ])
  })
})

function pollView(overrides: Partial<PollView> = {}): PollView {
  return {
    id: 'poll1',
    type: 'datetime',
    title: 'Team lunch',
    description: 'Bring cash',
    location: 'Kaffebrenneriet',
    timezone: OSLO,
    status: 'open',
    deadlineAt: '2026-06-14T10:00:00.000Z',
    finalizedOptionId: null,
    createdAt: '2026-06-01T00:00:00.000Z',
    settings: {
      requireParticipantEmail: true,
      allowComments: false,
      allowIfNeedBe: false,
      signupMaxClaims: 1,
    },
    notifications: null,
    owner: { name: 'Ada' },
    isOwner: true,
    options: [],
    participants: [],
    comments: [],
    scores: {},
    bestOptionId: null,
    claims: {},
    ...overrides,
  }
}

describe('draftFromPoll', () => {
  it('round-trips a datetime poll with one all-day and one time-slot option', () => {
    const poll = pollView({
      options: [
        {
          id: 'opt-date',
          position: 0,
          kind: 'date',
          startAt: '2026-06-15',
          endAt: null,
          label: null,
          capacity: null,
        },
        {
          id: 'opt-slot',
          position: 1,
          kind: 'datetime',
          // 09:00–10:30 Europe/Oslo on 2026-06-16 (CEST, UTC+2).
          startAt: '2026-06-16T07:00:00.000Z',
          endAt: '2026-06-16T08:30:00.000Z',
          label: null,
          capacity: null,
        },
      ],
    })

    const d = draftFromPoll(poll)

    expect(d.type).toBe('datetime')
    expect(d.title).toBe('Team lunch')
    expect(d.description).toBe('Bring cash')
    expect(d.location).toBe('Kaffebrenneriet')
    expect(d.timezone).toBe(OSLO)
    expect(d.deadlineAt).toBe('2026-06-14T10:00:00.000Z')
    expect(d.requireParticipantEmail).toBe(true)
    expect(d.allowComments).toBe(false)
    expect(d.allowIfNeedBe).toBe(false)
    expect(d.dates).toEqual([
      { id: 'opt-date', date: '2026-06-15', slots: [] },
      { date: '2026-06-16', slots: [{ id: 'opt-slot', start: '09:00', end: '10:30' }] },
    ])

    const input = draftToInput(d)
    expect(input.options).toEqual([
      { kind: 'date', date: '2026-06-15', id: 'opt-date' },
      {
        kind: 'datetime',
        startAt: '2026-06-16T07:00:00.000Z',
        endAt: '2026-06-16T08:30:00.000Z',
        id: 'opt-slot',
      },
    ])
  })

  it('round-trips a text-options poll', () => {
    const poll = pollView({
      type: 'options',
      options: [
        {
          id: 'opt-1',
          position: 0,
          kind: 'text',
          startAt: null,
          endAt: null,
          label: 'Pizza',
          capacity: null,
        },
        {
          id: 'opt-2',
          position: 1,
          kind: 'text',
          startAt: null,
          endAt: null,
          label: 'Sushi',
          capacity: null,
        },
      ],
    })

    const d = draftFromPoll(poll)

    expect(d.type).toBe('options')
    expect(d.textOptions).toEqual([
      { id: 'opt-1', label: 'Pizza' },
      { id: 'opt-2', label: 'Sushi' },
    ])

    const input = draftToInput(d)
    expect(input.options).toEqual([
      { kind: 'text', label: 'Pizza', id: 'opt-1' },
      { kind: 'text', label: 'Sushi', id: 'opt-2' },
    ])
  })

  it('round-trips a signup poll with per-slot capacities, including unlimited', () => {
    const poll = pollView({
      type: 'signup',
      settings: {
        requireParticipantEmail: false,
        allowComments: true,
        allowIfNeedBe: false,
        signupMaxClaims: 3,
      },
      options: [
        {
          id: 'opt-1',
          position: 0,
          kind: 'datetime',
          startAt: '2026-06-15T07:00:00.000Z',
          endAt: null,
          label: null,
          capacity: 1,
        },
        {
          id: 'opt-2',
          position: 1,
          kind: 'datetime',
          startAt: '2026-06-15T16:00:00.000Z',
          endAt: null,
          label: null,
          capacity: null,
        },
        {
          id: 'opt-3',
          position: 2,
          kind: 'datetime',
          startAt: '2026-06-16T07:00:00.000Z',
          endAt: null,
          label: null,
          capacity: 5,
        },
      ],
    })

    const d = draftFromPoll(poll)

    expect(d.type).toBe('signup')
    expect(d.signupMaxClaims).toBe(3)
    expect(d.dates).toEqual([
      {
        date: '2026-06-15',
        slots: [
          { id: 'opt-1', start: '09:00', end: null, capacity: 1 },
          { id: 'opt-2', start: '18:00', end: null, capacity: null },
        ],
      },
      { date: '2026-06-16', slots: [{ id: 'opt-3', start: '09:00', end: null, capacity: 5 }] },
    ])

    const input = draftToInput(d)
    expect(input.options.map((o) => o.capacity)).toEqual([1, null, 5])
    expect(input.signupMaxClaims).toBe(3)
    expect(input.allowIfNeedBe).toBe(false)
  })

  it('round-trips a signup poll with text options and capacities', () => {
    const poll = pollView({
      type: 'signup',
      settings: {
        requireParticipantEmail: false,
        allowComments: true,
        allowIfNeedBe: false,
        signupMaxClaims: 1,
      },
      options: [
        {
          id: 'opt-1',
          position: 0,
          kind: 'text',
          startAt: null,
          endAt: null,
          label: 'Setup',
          capacity: 2,
        },
        {
          id: 'opt-2',
          position: 1,
          kind: 'text',
          startAt: null,
          endAt: null,
          label: 'Cleanup',
          capacity: null,
        },
      ],
    })

    const d = draftFromPoll(poll)

    expect(d.textOptions).toEqual([
      { id: 'opt-1', label: 'Setup', capacity: 2 },
      { id: 'opt-2', label: 'Cleanup', capacity: null },
    ])

    const input = draftToInput(d)
    expect(input.options).toEqual([
      { kind: 'text', label: 'Setup', id: 'opt-1', capacity: 2 },
      { kind: 'text', label: 'Cleanup', id: 'opt-2', capacity: null },
    ])
  })
})
