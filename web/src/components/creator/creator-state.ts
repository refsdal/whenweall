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
export type DraftSlot = {
  id?: string
  start: string
  end: string | null
  /** Signup only: spots on this slot. `undefined` means the default of 1; `null` is unlimited. */
  capacity?: number | null
}

/** A selected day. No slots at all means the day itself is the option ("all day"). */
export type DraftDateOption = {
  id?: string
  date: string
  slots: DraftSlot[]
  /** Signup only, and only meaningful when `slots` is empty (the day itself is the option). */
  capacity?: number | null
}

/** A free-text option. `id` is set when it came from an existing poll option (see `DraftSlot`). */
export type DraftTextOption = {
  id?: string
  label: string
  /** Signup only: spots on this option. `undefined` means the default of 1; `null` is unlimited. */
  capacity?: number | null
}

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
  /** Signup only: how many slots one participant may claim (1..100). */
  signupMaxClaims: number
}

export type CreatorAction =
  | { type: 'setField'; field: keyof CreatorDraft; value: unknown }
  | { type: 'toggleDate'; date: string }
  | { type: 'addSlot'; date: string; start: string; end: string | null; capacity?: number | null }
  | { type: 'removeSlot'; date: string; index: number }
  | { type: 'applySlotsToAll'; fromDate: string }
  | { type: 'setTextOptions'; options: DraftTextOption[] }
  | { type: 'setSlotCapacity'; date: string; index: number; capacity: number | null }
  | { type: 'setDateCapacity'; date: string; capacity: number | null }
  | { type: 'setTextOptionCapacity'; index: number; capacity: number | null }
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
    signupMaxClaims: 1,
  }
}

/** True when the draft's options are (or, for a fresh signup draft, would be) free-text rows. */
function usesTextOptions(draft: Pick<CreatorDraft, 'type' | 'textOptions'>): boolean {
  return draft.type === 'options' || (draft.type === 'signup' && draft.textOptions.length > 0)
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
      const slot: DraftSlot = {
        start: action.start,
        end: action.end,
        ...(action.capacity !== undefined ? { capacity: action.capacity } : {}),
      }
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
        // more than one changed option onto the same preserved id. The capacity travels with the
        // copy (it is not identity, unlike the id), so a signup sheet's spot counts come along too.
        dates: draft.dates.map((day) =>
          day.date === action.fromDate
            ? day
            : {
                ...day,
                slots: source.slots.map((s) => ({
                  start: s.start,
                  end: s.end,
                  capacity: s.capacity,
                })),
              },
        ),
      }
    }

    case 'setTextOptions':
      return { ...draft, textOptions: action.options }

    case 'setSlotCapacity':
      return {
        ...draft,
        dates: mapDay(draft.dates, action.date, (slots) =>
          slots.map((slot, i) =>
            i === action.index ? { ...slot, capacity: action.capacity } : slot,
          ),
        ),
      }

    case 'setDateCapacity':
      return {
        ...draft,
        dates: draft.dates.map((day) =>
          day.date === action.date ? { ...day, capacity: action.capacity } : day,
        ),
      }

    case 'setTextOptionCapacity':
      return {
        ...draft,
        textOptions: draft.textOptions.map((option, i) =>
          i === action.index ? { ...option, capacity: action.capacity } : option,
        ),
      }

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
  if (usesTextOptions(draft)) {
    return draft.textOptions.filter((option) => option.label.trim().length > 0).length
  }
  return draft.dates.reduce((total, day) => total + Math.max(day.slots.length, 1), 0)
}

/** `undefined` (the signup default) becomes 1; `null` (unlimited) passes through unchanged. */
function normalizeCapacity(capacity: number | null | undefined): number | null {
  return capacity === null ? null : (capacity ?? 1)
}

/**
 * The creator summary's "N slots, M spots" line for a signup draft: the option count alongside
 * the sum of every option's capacity, or `null` for spots when any option is unlimited.
 */
export function signupCapacitySummary(draft: CreatorDraft): {
  slots: number
  spots: number | null
} {
  const capacities: (number | null | undefined)[] = usesTextOptions(draft)
    ? draft.textOptions.map((option) => option.capacity)
    : draft.dates.flatMap((day) =>
        day.slots.length === 0 ? [day.capacity] : day.slots.map((slot) => slot.capacity),
      )

  const spots = capacities.some((c) => c === null)
    ? null
    : capacities.reduce((sum: number, c) => sum + (c ?? 1), 0)

  return { slots: countOptions(draft), spots }
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

function datetimeOption(
  date: string,
  slot: DraftSlot,
  timezone: string,
  capacityField: (capacity: number | null | undefined) => { capacity?: number | null },
): OptionInput {
  const idField = slot.id ? { id: slot.id } : {}
  const startAt = localToUtcIso(date, slot.start, timezone)
  if (!slot.end) {
    return { kind: 'datetime', startAt, endAt: null, ...idField, ...capacityField(slot.capacity) }
  }

  let endAt = localToUtcIso(date, slot.end, timezone)
  // "22:00 – 01:00" means the party ends after midnight, not that it ends before it began.
  if (new Date(endAt).getTime() <= new Date(startAt).getTime()) {
    endAt = localToUtcIso(nextDay(date), slot.end, timezone)
  }
  return { kind: 'datetime', startAt, endAt, ...idField, ...capacityField(slot.capacity) }
}

function optionsFor(draft: CreatorDraft): OptionInput[] {
  const isSignup = draft.type === 'signup'
  // Capacity is only a signup concept — for every other poll type this contributes nothing to
  // the option, so `capacity` never appears on the payload.
  const capacityField = (capacity: number | null | undefined): { capacity?: number | null } =>
    isSignup ? { capacity: normalizeCapacity(capacity) } : {}

  if (usesTextOptions(draft)) {
    return draft.textOptions
      .map((option) => ({ id: option.id, label: option.label.trim(), capacity: option.capacity }))
      .filter((option) => option.label.length > 0)
      .map((option) => ({
        kind: 'text',
        label: option.label,
        ...(option.id ? { id: option.id } : {}),
        ...capacityField(option.capacity),
      }))
  }

  return [...draft.dates].sort(byDate).flatMap((day) => {
    if (day.slots.length === 0) {
      return [
        {
          kind: 'date',
          date: day.date,
          ...(day.id ? { id: day.id } : {}),
          ...capacityField(day.capacity),
        } as OptionInput,
      ]
    }
    return day.slots.map((slot) => datetimeOption(day.date, slot, draft.timezone, capacityField))
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
  const isSignup = draft.type === 'signup'
  return {
    type: draft.type,
    title: draft.title.trim(),
    description: trimmedOrUndefined(draft.description),
    location: trimmedOrUndefined(draft.location),
    timezone: draft.timezone,
    deadlineAt: draft.deadlineAt,
    requireParticipantEmail: draft.requireParticipantEmail,
    allowComments: draft.allowComments,
    // "If need be" has no meaning when claiming a slot is binary (claimed or not).
    allowIfNeedBe: isSignup ? false : draft.allowIfNeedBe,
    options: optionsFor(draft),
    ...(isSignup ? { signupMaxClaims: draft.signupMaxClaims } : {}),
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
  // Capacity is only meaningful (and only ever set) on a signup poll's options — carrying it
  // through for other types would put a stray `capacity: null` on drafts that never had one.
  const isSignup = poll.type === 'signup'
  const capacityField = (capacity: number | null): { capacity?: number | null } =>
    isSignup ? { capacity } : {}
  // A signup sheet's options are date, datetime or text but never mixed (same rule as v1's
  // datetime/options types), so the first option's kind tells us which editor to seed.
  const isTextPoll = poll.type === 'options' || (isSignup && orderedOptions[0]?.kind === 'text')

  if (isTextPoll) {
    for (const option of orderedOptions) {
      textOptions.push({
        id: option.id,
        label: option.label ?? '',
        ...capacityField(option.capacity),
      })
    }
  } else {
    const byDateKey = new Map<string, DraftDateOption>()

    for (const option of orderedOptions) {
      if (option.kind === 'date') {
        // The date-kind option's `startAt` field holds the plain `YYYY-MM-DD` date, not an ISO
        // instant — see `optionRowFields` in `service.ts`.
        const date = option.startAt as string
        byDateKey.set(date, {
          id: option.id,
          date,
          slots: [],
          ...capacityField(option.capacity),
        })
        continue
      }
      if (option.kind !== 'datetime') continue

      const { date, time: start } = utcIsoToLocalParts(option.startAt as string, poll.timezone)
      const end = option.endAt ? utcIsoToLocalParts(option.endAt, poll.timezone).time : null
      const slot: DraftSlot = { id: option.id, start, end, ...capacityField(option.capacity) }

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
    signupMaxClaims: poll.settings.signupMaxClaims,
  }
}
