import { localToUtcIso, utcIsoToLocalParts } from '#/lib/time'
import { LIMITS, type CreatePollInput, type OptionInput } from '#/server/polls/schemas'
import type { PollType } from '#/server/db/schema'
import type { PollView } from '#/server/polls/viewmodel'

/**
 * A single time window on a day. `end` is optional — "18:00" alone means "at 18:00". `id` is set
 * when the slot came from an existing poll option (via `draftFromPoll`); it lets `draftToInput`
 * tell `updatePoll` "this is the same option", so the option keeps its votes instead of being
 * deleted and recreated.
 */
export type DraftSlot = { id?: string; start: string; end: string | null }

/** A selected day. No slots at all means the day itself is the option ("all day"). */
export type DraftDateOption = { id?: string; date: string; slots: DraftSlot[] }

/** A free-text option. `id` is set when it came from an existing poll option (see `DraftSlot`). */
export type DraftTextOption = { id?: string; label: string }

export type CreatorDraft = {
  step: 0 | 1 | 2
  type: PollType
  title: string
  description: string
  location: string
  timezone: string
  dates: DraftDateOption[]
  textOptions: DraftTextOption[]
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
  | { type: 'setTextOptions'; options: DraftTextOption[] }
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
        // The source day keeps its own slots (and their ids) untouched. Every other day gets a
        // fresh, id-less copy: those slots become new options rather than the source's existing
        // ones being moved onto a different date, which would collide when `updatePoll` batches
        // more than one changed option onto the same preserved id.
        dates: draft.dates.map((day) =>
          day.date === action.fromDate
            ? day
            : { ...day, slots: source.slots.map((s) => ({ start: s.start, end: s.end })) },
        ),
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
    return draft.textOptions.filter((option) => option.label.trim().length > 0).length
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
  const idField = slot.id ? { id: slot.id } : {}
  const startAt = localToUtcIso(date, slot.start, timezone)
  if (!slot.end) return { kind: 'datetime', startAt, endAt: null, ...idField }

  let endAt = localToUtcIso(date, slot.end, timezone)
  // "22:00 – 01:00" means the party ends after midnight, not that it ends before it began.
  if (new Date(endAt).getTime() <= new Date(startAt).getTime()) {
    endAt = localToUtcIso(nextDay(date), slot.end, timezone)
  }
  return { kind: 'datetime', startAt, endAt, ...idField }
}

function optionsFor(draft: CreatorDraft): OptionInput[] {
  if (draft.type === 'options') {
    return draft.textOptions
      .map((option) => ({ id: option.id, label: option.label.trim() }))
      .filter((option) => option.label.length > 0)
      .map((option) => ({
        kind: 'text',
        label: option.label,
        ...(option.id ? { id: option.id } : {}),
      }))
  }

  return [...draft.dates].sort(byDate).flatMap((day) => {
    if (day.slots.length === 0) {
      return [{ kind: 'date', date: day.date, ...(day.id ? { id: day.id } : {}) } as OptionInput]
    }
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

/**
 * The inverse of `draftToInput`, seeding the editor from an existing poll (task 19). Every option
 * keeps its id, so re-submitting an untouched draft round-trips to the same options and none of
 * the votes on them are lost.
 */
export function draftFromPoll(poll: PollView): CreatorDraft {
  const dates: DraftDateOption[] = []
  const textOptions: DraftTextOption[] = []

  const orderedOptions = [...poll.options].sort((a, b) => a.position - b.position)

  if (poll.type === 'options') {
    for (const option of orderedOptions) {
      textOptions.push({ id: option.id, label: option.label ?? '' })
    }
  } else {
    const byDateKey = new Map<string, DraftDateOption>()

    for (const option of orderedOptions) {
      if (option.kind === 'date') {
        // The date-kind option's `startAt` field holds the plain `YYYY-MM-DD` date, not an ISO
        // instant — see `optionRowFields` in `service.ts`.
        const date = option.startAt as string
        byDateKey.set(date, { id: option.id, date, slots: [] })
        continue
      }
      if (option.kind !== 'datetime') continue

      const { date, time: start } = utcIsoToLocalParts(option.startAt as string, poll.timezone)
      const end = option.endAt ? utcIsoToLocalParts(option.endAt, poll.timezone).time : null
      const slot: DraftSlot = { id: option.id, start, end }

      const existing = byDateKey.get(date)
      if (existing) existing.slots.push(slot)
      else byDateKey.set(date, { date, slots: [slot] })
    }

    dates.push(...[...byDateKey.values()].sort(byDate))
    for (const day of dates) day.slots.sort(bySlot)
  }

  return {
    step: 0,
    type: poll.type,
    title: poll.title,
    description: poll.description ?? '',
    location: poll.location ?? '',
    timezone: poll.timezone,
    dates,
    textOptions,
    deadlineAt: poll.deadlineAt,
    requireParticipantEmail: poll.settings.requireParticipantEmail,
    allowComments: poll.settings.allowComments,
    allowIfNeedBe: poll.settings.allowIfNeedBe,
  }
}
