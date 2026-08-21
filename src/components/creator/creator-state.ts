import { localToUtcIso } from '#/lib/time'
import { LIMITS, type CreatePollInput, type OptionInput } from '#/server/polls/schemas'
import type { PollType } from '#/server/db/schema'

/** A single time window on a day. `end` is optional — "18:00" alone means "at 18:00". */
export type DraftSlot = { start: string; end: string | null }

/** A selected day. No slots at all means the day itself is the option ("all day"). */
export type DraftDateOption = { date: string; slots: DraftSlot[] }

export type CreatorDraft = {
  step: 0 | 1 | 2
  type: PollType
  title: string
  description: string
  location: string
  timezone: string
  dates: DraftDateOption[]
  textOptions: string[]
  deadlineAt: string | null
  requireParticipantEmail: boolean
  allowComments: boolean
  allowIfNeedBe: boolean
}

export type CreatorAction =
  | { type: 'setField'; field: keyof CreatorDraft; value: unknown }
  | { type: 'toggleDate'; date: string }
  | { type: 'addSlot'; date: string; start: string; end: string | null }
  | { type: 'removeSlot'; date: string; index: number }
  | { type: 'applySlotsToAll'; fromDate: string }
  | { type: 'setTextOptions'; options: string[] }
  | { type: 'next' }
  | { type: 'back' }

const LAST_STEP = 2

export function initialDraft(timezone: string): CreatorDraft {
  return {
    step: 0,
    type: 'datetime',
    title: '',
    description: '',
    location: '',
    timezone,
    dates: [],
    textOptions: [],
    deadlineAt: null,
    // The friendly defaults: maybes and comments make a poll more useful, while asking every
    // participant for an email makes it heavier — so that one is opt-in.
    requireParticipantEmail: false,
    allowComments: true,
    allowIfNeedBe: true,
  }
}

function byDate(a: DraftDateOption, b: DraftDateOption): number {
  return a.date < b.date ? -1 : a.date > b.date ? 1 : 0
}

function bySlot(a: DraftSlot, b: DraftSlot): number {
  if (a.start !== b.start) return a.start < b.start ? -1 : 1
  return (a.end ?? '').localeCompare(b.end ?? '')
}

/** Replaces the slots of one selected day, leaving the other days untouched. */
function mapDay(
  dates: DraftDateOption[],
  date: string,
  fn: (slots: DraftSlot[]) => DraftSlot[],
): DraftDateOption[] {
  return dates.map((day) => (day.date === date ? { ...day, slots: fn(day.slots) } : day))
}

export function creatorReducer(draft: CreatorDraft, action: CreatorAction): CreatorDraft {
  switch (action.type) {
    case 'setField':
      return { ...draft, [action.field]: action.value }

    case 'toggleDate': {
      const selected = draft.dates.some((d) => d.date === action.date)
      const dates = selected
        ? draft.dates.filter((d) => d.date !== action.date)
        : [...draft.dates, { date: action.date, slots: [] }].sort(byDate)
      return { ...draft, dates }
    }

    case 'addSlot': {
      const day = draft.dates.find((d) => d.date === action.date)
      if (!day) return draft
      const slot: DraftSlot = { start: action.start, end: action.end }
      // An exact duplicate would be rejected by the server as a duplicate option, so it is
      // dropped here rather than surfacing as a validation error three clicks later.
      if (day.slots.some((s) => s.start === slot.start && s.end === slot.end)) return draft
      return {
        ...draft,
        dates: mapDay(draft.dates, action.date, (slots) => [...slots, slot].sort(bySlot)),
      }
    }

    case 'removeSlot':
      return {
        ...draft,
        dates: mapDay(draft.dates, action.date, (slots) =>
          slots.filter((_, i) => i !== action.index),
        ),
      }

    case 'applySlotsToAll': {
      const source = draft.dates.find((d) => d.date === action.fromDate)
      if (!source) return draft
      return {
        ...draft,
        // Each day gets its own copy of the slot objects so a later edit to one day can never
        // reach into another.
        dates: draft.dates.map((day) => ({ ...day, slots: source.slots.map((s) => ({ ...s })) })),
      }
    }

    case 'setTextOptions':
      return { ...draft, textOptions: action.options }

    case 'next':
      if (draft.step === LAST_STEP || !canAdvance(draft)) return draft
      return { ...draft, step: (draft.step + 1) as CreatorDraft['step'] }

    case 'back':
      if (draft.step === 0) return draft
      return { ...draft, step: (draft.step - 1) as CreatorDraft['step'] }
  }
}

/** How many options the current draft would create — what the "N options" badge shows. */
export function countOptions(draft: CreatorDraft): number {
  if (draft.type === 'options') {
    return draft.textOptions.filter((option) => option.trim().length > 0).length
  }
  return draft.dates.reduce((total, day) => total + Math.max(day.slots.length, 1), 0)
}

export function canAdvance(draft: CreatorDraft): boolean {
  if (draft.step === 0) return draft.title.trim().length > 0
  if (draft.step === 1) {
    const count = countOptions(draft)
    return count > 0 && count <= LIMITS.options
  }
  return true
}

/** `2026-06-15` → `2026-06-16`, used when a slot's end time wraps past midnight. */
function nextDay(date: string): string {
  const [y, m, d] = date.split('-').map(Number) as [number, number, number]
  const next = new Date(Date.UTC(y, m - 1, d + 1))
  return next.toISOString().slice(0, 10)
}

function datetimeOption(date: string, slot: DraftSlot, timezone: string): OptionInput {
  const startAt = localToUtcIso(date, slot.start, timezone)
  if (!slot.end) return { kind: 'datetime', startAt, endAt: null }

  let endAt = localToUtcIso(date, slot.end, timezone)
  // "22:00 – 01:00" means the party ends after midnight, not that it ends before it began.
  if (new Date(endAt).getTime() <= new Date(startAt).getTime()) {
    endAt = localToUtcIso(nextDay(date), slot.end, timezone)
  }
  return { kind: 'datetime', startAt, endAt }
}

function optionsFor(draft: CreatorDraft): OptionInput[] {
  if (draft.type === 'options') {
    return draft.textOptions
      .map((label) => label.trim())
      .filter((label) => label.length > 0)
      .map((label) => ({ kind: 'text', label }))
  }

  return [...draft.dates].sort(byDate).flatMap((day) => {
    if (day.slots.length === 0) return [{ kind: 'date', date: day.date } as OptionInput]
    return day.slots.map((slot) => datetimeOption(day.date, slot, draft.timezone))
  })
}

function trimmedOrUndefined(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed.length > 0 ? trimmed : undefined
}

/**
 * Turns the wizard's draft into the payload `createPoll` validates. Task 19 (poll editing)
 * reuses the same editors, so the conversion lives here rather than in the submit handler.
 */
export function draftToInput(draft: CreatorDraft): CreatePollInput {
  return {
    type: draft.type,
    title: draft.title.trim(),
    description: trimmedOrUndefined(draft.description),
    location: trimmedOrUndefined(draft.location),
    timezone: draft.timezone,
    deadlineAt: draft.deadlineAt,
    requireParticipantEmail: draft.requireParticipantEmail,
    allowComments: draft.allowComments,
    allowIfNeedBe: draft.allowIfNeedBe,
    options: optionsFor(draft),
  }
}
