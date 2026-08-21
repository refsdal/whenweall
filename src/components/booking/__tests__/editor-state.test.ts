import { describe, expect, it } from 'vitest'
import {
  canSave,
  dayIssues,
  draftFromPage,
  draftIssues,
  draftToInput,
  draftToUpdate,
  editorReducer,
  initialDraft,
  type EditorDraft,
} from '#/components/booking/editor-state'
import type { PageView } from '#/server/bookings/viewmodel'

function draftWith(overrides: Partial<EditorDraft> = {}): EditorDraft {
  return { ...initialDraft('Europe/Oslo'), title: 'Intro call', slug: 'intro-call', ...overrides }
}

describe('initialDraft', () => {
  it('opens Monday to Friday, 09:00–17:00, with the weekend off', () => {
    const draft = initialDraft('Europe/Oslo')

    for (const weekday of ['1', '2', '3', '4', '5']) {
      expect(draft.availability[weekday]).toEqual([{ start: '09:00', end: '17:00' }])
    }
    expect(draft.availability['6']).toEqual([])
    expect(draft.availability['0']).toEqual([])
  })

  it('uses the friendly scheduling defaults', () => {
    const draft = initialDraft('Europe/Oslo')

    expect(draft).toMatchObject({
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      bufferBeforeMin: 0,
      bufferAfterMin: 0,
      minNoticeMin: 120,
      maxDaysAhead: 60,
      googleSync: false,
      reminders: true,
      status: 'active',
    })
    expect(draft.dateOverrides).toEqual({})
  })

  it('does not share range objects between weekdays', () => {
    const draft = initialDraft('UTC')

    expect(draft.availability['1']?.[0]).not.toBe(draft.availability['2']?.[0])
  })
})

describe('editorReducer', () => {
  it('sets a scalar field', () => {
    const next = editorReducer(draftWith(), { type: 'setField', field: 'title', value: 'Coffee' })

    expect(next.title).toBe('Coffee')
  })

  it('replaces one day’s ranges and leaves the others alone', () => {
    const next = editorReducer(draftWith(), {
      type: 'setDayRanges',
      weekday: '1',
      ranges: [{ start: '10:00', end: '11:00' }],
    })

    expect(next.availability['1']).toEqual([{ start: '10:00', end: '11:00' }])
    expect(next.availability['2']).toEqual([{ start: '09:00', end: '17:00' }])
  })

  it('adds a default workday range to a day that was off', () => {
    const next = editorReducer(draftWith(), { type: 'addRange', weekday: '6' })

    expect(next.availability['6']).toEqual([{ start: '09:00', end: '17:00' }])
  })

  it('adds a following range that starts where the last one ended', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '1': [{ start: '09:00', end: '12:00' }],
      },
    })

    const next = editorReducer(draft, { type: 'addRange', weekday: '1' })

    expect(next.availability['1']).toEqual([
      { start: '09:00', end: '12:00' },
      { start: '12:00', end: '13:00' },
    ])
  })

  it('does not add a range when the day is already booked out to the evening', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '1': [{ start: '09:00', end: '23:45' }],
      },
    })

    const next = editorReducer(draft, { type: 'addRange', weekday: '1' })

    expect(next.availability['1']).toHaveLength(1)
  })

  it('removes a range by index', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '1': [
          { start: '09:00', end: '12:00' },
          { start: '13:00', end: '17:00' },
        ],
      },
    })

    const next = editorReducer(draft, { type: 'removeRange', weekday: '1', index: 0 })

    expect(next.availability['1']).toEqual([{ start: '13:00', end: '17:00' }])
  })

  it('copies one day’s ranges to every other weekday', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '2': [{ start: '08:00', end: '10:00' }],
      },
    })

    const next = editorReducer(draft, { type: 'copyDayToAll', weekday: '2' })

    for (const weekday of ['0', '1', '2', '3', '4', '5', '6']) {
      expect(next.availability[weekday]).toEqual([{ start: '08:00', end: '10:00' }])
    }
  })

  it('copies ranges by value, so editing one day afterwards does not change another', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '2': [{ start: '08:00', end: '10:00' }],
      },
    })

    const next = editorReducer(draft, { type: 'copyDayToAll', weekday: '2' })

    expect(next.availability['3']?.[0]).not.toBe(next.availability['2']?.[0])
  })

  it('sets a date override, marks a day off with an empty list, and removes it with null', () => {
    const ranges = [{ start: '10:00', end: '14:00' }]

    const withRanges = editorReducer(draftWith(), {
      type: 'setOverride',
      date: '2026-09-01',
      ranges,
    })
    expect(withRanges.dateOverrides['2026-09-01']).toEqual(ranges)

    const dayOff = editorReducer(withRanges, {
      type: 'setOverride',
      date: '2026-09-01',
      ranges: [],
    })
    expect(dayOff.dateOverrides['2026-09-01']).toEqual([])

    const removed = editorReducer(dayOff, {
      type: 'setOverride',
      date: '2026-09-01',
      ranges: null,
    })
    expect(removed.dateOverrides).not.toHaveProperty('2026-09-01')
  })

  it('replaces the whole draft on reset', () => {
    const next = editorReducer(draftWith(), { type: 'reset', draft: initialDraft('UTC') })

    expect(next.timezone).toBe('UTC')
    expect(next.title).toBe('')
  })
})

describe('dayIssues', () => {
  it('accepts aligned, ordered, non-overlapping ranges', () => {
    expect(
      dayIssues([
        { start: '09:00', end: '12:00' },
        { start: '13:15', end: '17:45' },
      ]),
    ).toEqual([])
  })

  it('flags a time that is not on the 15-minute grid', () => {
    expect(dayIssues([{ start: '09:07', end: '12:00' }])).toEqual([{ index: 0, code: 'unaligned' }])
  })

  it('flags a range that ends before it starts', () => {
    expect(dayIssues([{ start: '12:00', end: '09:00' }])).toEqual([{ index: 0, code: 'order' }])
  })

  it('flags a range that ends exactly when it starts', () => {
    expect(dayIssues([{ start: '12:00', end: '12:00' }])).toEqual([{ index: 0, code: 'order' }])
  })

  it('flags overlapping ranges regardless of the order they were entered in', () => {
    expect(
      dayIssues([
        { start: '13:00', end: '17:00' },
        { start: '09:00', end: '14:00' },
      ]),
    ).toEqual([{ index: 0, code: 'overlap' }])
  })

  it('does not flag ranges that merely touch', () => {
    expect(
      dayIssues([
        { start: '09:00', end: '12:00' },
        { start: '12:00', end: '17:00' },
      ]),
    ).toEqual([])
  })
})

describe('draftIssues', () => {
  it('is empty for a clean draft', () => {
    expect(draftIssues(draftWith())).toEqual({})
  })

  it('reports issues per weekday key', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '3': [{ start: '17:00', end: '09:00' }],
      },
    })

    expect(draftIssues(draft)).toEqual({ '3': [{ index: 0, code: 'order' }] })
  })

  it('reports issues on date overrides too', () => {
    const draft = draftWith({ dateOverrides: { '2026-09-01': [{ start: '09:07', end: '10:00' }] } })

    expect(draftIssues(draft)).toEqual({ '2026-09-01': [{ index: 0, code: 'unaligned' }] })
  })
})

describe('draftToInput', () => {
  it('drops days with no ranges, sorts the rest and trims text', () => {
    const draft = draftWith({
      description: '  A quick chat  ',
      location: '  Zoom  ',
      availability: {
        ...initialDraft('UTC').availability,
        '1': [
          { start: '13:00', end: '17:00' },
          { start: '09:00', end: '12:00' },
        ],
        '6': [],
      },
    })

    const input = draftToInput(draft)

    expect(input).not.toBeNull()
    expect(input?.availability['1']).toEqual([
      { start: '09:00', end: '12:00' },
      { start: '13:00', end: '17:00' },
    ])
    expect(input?.availability).not.toHaveProperty('6')
    expect(input?.description).toBe('A quick chat')
    expect(input?.location).toBe('Zoom')
  })

  it('omits description, location and overrides when they are empty', () => {
    const input = draftToInput(draftWith())

    expect(input?.description).toBeUndefined()
    expect(input?.location).toBeUndefined()
    expect(input?.dateOverrides).toBeUndefined()
  })

  it('keeps a day-off override', () => {
    const input = draftToInput(draftWith({ dateOverrides: { '2026-09-01': [] } }))

    expect(input?.dateOverrides).toEqual({ '2026-09-01': [] })
  })

  it('returns null when a day overlaps itself', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '1': [
          { start: '09:00', end: '14:00' },
          { start: '13:00', end: '17:00' },
        ],
      },
    })

    expect(draftToInput(draft)).toBeNull()
  })

  it('returns null when the title is blank or the slug is invalid', () => {
    expect(draftToInput(draftWith({ title: '   ' }))).toBeNull()
    expect(draftToInput(draftWith({ slug: 'Not A Slug' }))).toBeNull()
  })

  it('returns null when every day is off', () => {
    const empty = Object.fromEntries(['0', '1', '2', '3', '4', '5', '6'].map((d) => [d, []]))
    expect(draftToInput(draftWith({ availability: empty }))).toBeNull()
  })
})

describe('draftToUpdate', () => {
  it('carries the page id, the status and cleared text fields', () => {
    const payload = draftToUpdate(draftWith({ status: 'paused' }), 'page123')

    expect(payload).toMatchObject({
      pageId: 'page123',
      status: 'paused',
      description: '',
      location: '',
    })
  })

  it('returns null for a draft that would not validate', () => {
    expect(draftToUpdate(draftWith({ slug: 'x' }), 'page123')).toBeNull()
  })
})

function pageFixture(overrides: Partial<PageView> = {}): PageView {
  return {
    id: 'page123',
    slug: 'intro-call',
    title: 'Intro call',
    description: 'A quick chat',
    location: 'Zoom',
    timezone: 'Europe/Oslo',
    slotDurationMin: 45,
    bufferBeforeMin: 15,
    bufferAfterMin: 10,
    minNoticeMin: 240,
    maxDaysAhead: 30,
    availability: {
      '1': [
        { start: '09:00', end: '12:00' },
        { start: '13:00', end: '17:00' },
      ],
      '3': [{ start: '10:00', end: '15:00' }],
    },
    dateOverrides: { '2026-09-01': [], '2026-09-02': [{ start: '10:00', end: '12:00' }] },
    googleSync: true,
    reminders: false,
    status: 'paused',
    createdAt: '2026-08-01T10:00:00.000Z',
    updatedAt: '2026-08-01T10:00:00.000Z',
    ...overrides,
  }
}

describe('draftFromPage', () => {
  it('seeds every field, filling the days the page has no ranges for', () => {
    const draft = draftFromPage(pageFixture())

    expect(draft).toMatchObject({
      slug: 'intro-call',
      title: 'Intro call',
      description: 'A quick chat',
      location: 'Zoom',
      timezone: 'Europe/Oslo',
      slotDurationMin: 45,
      bufferBeforeMin: 15,
      bufferAfterMin: 10,
      minNoticeMin: 240,
      maxDaysAhead: 30,
      googleSync: true,
      reminders: false,
      status: 'paused',
    })
    expect(draft.availability['2']).toEqual([])
    expect(draft.dateOverrides['2026-09-01']).toEqual([])
  })

  it('turns a null description, location and overrides into empty values', () => {
    const draft = draftFromPage(
      pageFixture({ description: null, location: null, dateOverrides: null }),
    )

    expect(draft.description).toBe('')
    expect(draft.location).toBe('')
    expect(draft.dateOverrides).toEqual({})
  })

  it('round-trips a page through the draft unchanged', () => {
    const page = pageFixture()

    const input = draftToInput(draftFromPage(page))

    expect(input).toEqual({
      slug: page.slug,
      title: page.title,
      description: page.description,
      location: page.location,
      timezone: page.timezone,
      slotDurationMin: page.slotDurationMin,
      bufferBeforeMin: page.bufferBeforeMin,
      bufferAfterMin: page.bufferAfterMin,
      minNoticeMin: page.minNoticeMin,
      maxDaysAhead: page.maxDaysAhead,
      availability: page.availability,
      dateOverrides: page.dateOverrides,
      googleSync: page.googleSync,
      reminders: page.reminders,
    })
  })
})

describe('canSave', () => {
  it('is true for a valid draft', () => {
    expect(canSave(draftWith())).toBe(true)
  })

  it('is false while the title is empty', () => {
    expect(canSave(draftWith({ title: '' }))).toBe(false)
  })

  it('is false while a day overlaps itself', () => {
    const draft = draftWith({
      availability: {
        ...initialDraft('UTC').availability,
        '1': [
          { start: '09:00', end: '14:00' },
          { start: '13:00', end: '17:00' },
        ],
      },
    })

    expect(canSave(draft)).toBe(false)
  })
})
