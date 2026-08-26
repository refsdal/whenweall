import { useState, type KeyboardEvent, type ReactNode } from 'react'
import { motion } from 'motion/react'
import { CopyPlus, Plus, X } from 'lucide-react'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { CapacityField } from '#/components/creator/CapacityField'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'
import type { DraftSlot } from '#/components/creator/creator-state'

/**
 * Chips animate in but not out: a chip that is still exiting would keep a clickable remove
 * button whose captured index no longer matches the list, so removals stay instant.
 */
const enter = {
  initial: { opacity: 0, scale: 0.92 },
  animate: { opacity: 1, scale: 1 },
  transition: spring,
}

/** Common start times offered as one-tap chips above the typeable start field. */
const QUICK_STARTS = ['08:00', '10:00', '12:00', '17:00', '18:00', '19:00', '20:00']

type DurationId = '30' | '60' | '90' | '120' | '180' | 'custom' | 'open'

/** The duration chips that replace the plain end-time field, in display order. */
const DURATIONS: { id: DurationId; minutes: number | null; label: () => string }[] = [
  { id: '30', minutes: 30, label: () => m.creator_duration_30() },
  { id: '60', minutes: 60, label: () => m.creator_duration_60() },
  { id: '90', minutes: 90, label: () => m.creator_duration_90() },
  { id: '120', minutes: 120, label: () => m.creator_duration_120() },
  { id: '180', minutes: 180, label: () => m.creator_duration_180() },
  { id: 'custom', minutes: null, label: () => m.creator_duration_custom() },
  { id: 'open', minutes: null, label: () => m.creator_duration_open() },
]

/** The duration a freshly mounted editor starts with, before the picker changes it. */
const DEFAULT_DURATION: DurationId = '60'

function slotLabel(slot: DraftSlot): string {
  return slot.end ? `${slot.start} – ${slot.end}` : slot.start
}

/** True when the end time is at or before the start, i.e. the window runs past midnight. */
function endsNextDay(slot: DraftSlot): boolean {
  return slot.end !== null && slot.end <= slot.start
}

/** `"23:30"` + 60 minutes → `"00:30"`, wrapping past midnight rather than overflowing. */
function addMinutes(time: string, minutes: number): string {
  const [hours, mins] = time.split(':').map(Number) as [number, number]
  const total = (((hours * 60 + mins + minutes) % 1440) + 1440) % 1440
  const h = String(Math.floor(total / 60)).padStart(2, '0')
  const m2 = String(total % 60).padStart(2, '0')
  return `${h}:${m2}`
}

/** Derives the end time (or `null`) a chosen duration produces from a given start. */
function deriveEnd(start: string, duration: DurationId, customEnd: string): string | null {
  if (duration === 'open') return null
  if (duration === 'custom') return customEnd || null
  if (!start) return null
  const minutes = DURATIONS.find((d) => d.id === duration)?.minutes
  return minutes ? addMinutes(start, minutes) : null
}

/** A toggle-style pill shared by the quick-start and duration chip rows. */
function Chip({
  selected,
  onClick,
  children,
}: {
  selected: boolean
  onClick: () => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onClick}
      className={cn(
        'focus-ring inline-flex h-8 items-center rounded-full border px-3 text-xs font-medium tabular-nums transition-colors',
        selected
          ? 'border-primary bg-accent-soft text-accent-foreground'
          : 'border-border text-muted-foreground hover:text-foreground',
      )}
    >
      {children}
    </button>
  )
}

/**
 * The time windows on a single day: existing slots as removable chips, plus a start/optional-end
 * pair to add another. A day with no slots stays an all-day option.
 */
export function TimeSlotEditor({
  date,
  slots,
  onAdd,
  onRemove,
  onApplyToAll,
  showCapacity = false,
  onSetCapacity,
}: {
  date: string
  slots: DraftSlot[]
  onAdd: (start: string, end: string | null, capacity?: number | null) => void
  onRemove: (index: number) => void
  onApplyToAll?: () => void
  /** Signup only: shows a capacity field per slot, and on the add-slot form. */
  showCapacity?: boolean
  onSetCapacity?: (index: number, capacity: number | null) => void
}) {
  const reduceMotion = useReducedMotion()
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [duration, setDuration] = useState<DurationId>(DEFAULT_DURATION)
  const [capacity, setCapacity] = useState<number | null>(1)

  const startId = `slot-start-${date}`
  const endId = `slot-end-${date}`
  const capacityId = `slot-capacity-${date}`

  const computedEnd = deriveEnd(start, duration, end)

  function add() {
    if (!start) return
    if (showCapacity) onAdd(start, computedEnd, capacity)
    else onAdd(start, computedEnd)
    // The duration sticks (so consecutive adds are tap → add → tap → add); only the start (and,
    // for a custom end, the now-stale absolute end time) resets.
    setStart('')
    if (duration === 'custom') setEnd('')
    setCapacity(1)
  }

  /**
   * The end field's options should begin at the newly chosen start time (so "the first available
   * option is the start time" per the add-slot form), and an already-entered end that is now
   * earlier than the start no longer makes sense as a plain same-day window — bump it up to the
   * new minimum rather than leaving a stale, invalid-looking value behind.
   */
  function handleStartChange(value: string) {
    setStart(value)
    if (end && end < value) setEnd(value)
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== 'Enter') return
    event.preventDefault()
    add()
  }

  return (
    <div className="flex flex-col gap-2.5">
      {slots.length === 0 ? (
        <p className="text-xs text-muted-foreground italic">{m.creator_all_day_hint()}</p>
      ) : showCapacity ? (
        <ul className="flex flex-col gap-1.5">
          {slots.map((slot, index) => (
            <motion.li
              key={`${slot.start}-${slot.end ?? ''}`}
              layout={!reduceMotion}
              {...(reduceMotion ? {} : enter)}
              className="flex flex-wrap items-center gap-2 rounded-lg bg-accent-soft px-2.5 py-1.5 text-xs font-medium text-accent-foreground"
            >
              <span className="tabular-nums">{slotLabel(slot)}</span>
              {endsNextDay(slot) && (
                <span title={m.creator_slot_next_day()} className="opacity-70">
                  +1
                </span>
              )}
              <CapacityField
                id={`slot-capacity-${date}-${index}`}
                size="sm"
                value={slot.capacity ?? 1}
                onChange={(next) => onSetCapacity?.(index, next)}
              />
              <button
                type="button"
                aria-label={m.creator_slot_remove({ time: slotLabel(slot) })}
                onClick={() => onRemove(index)}
                className="focus-ring ml-auto inline-flex size-5 shrink-0 items-center justify-center rounded-full transition-colors hover:bg-black/10 dark:hover:bg-white/15"
              >
                <X aria-hidden="true" className="size-3" />
              </button>
            </motion.li>
          ))}
        </ul>
      ) : (
        <ul className="flex flex-wrap items-center gap-1.5">
          {slots.map((slot, index) => (
            <motion.li
              key={`${slot.start}-${slot.end ?? ''}`}
              layout={!reduceMotion}
              {...(reduceMotion ? {} : enter)}
              className="inline-flex items-center gap-1 rounded-full bg-accent-soft py-0.5 pr-0.5 pl-2.5 text-xs font-medium text-accent-foreground"
            >
              <span className="tabular-nums">{slotLabel(slot)}</span>
              {endsNextDay(slot) && (
                <span title={m.creator_slot_next_day()} className="opacity-70">
                  +1
                </span>
              )}
              <button
                type="button"
                aria-label={m.creator_slot_remove({ time: slotLabel(slot) })}
                onClick={() => onRemove(index)}
                className="focus-ring inline-flex size-5 items-center justify-center rounded-full transition-colors hover:bg-black/10 dark:hover:bg-white/15"
              >
                <X aria-hidden="true" className="size-3" />
              </button>
            </motion.li>
          ))}
        </ul>
      )}

      <div className="flex flex-wrap gap-1.5">
        {QUICK_STARTS.map((time) => (
          <Chip key={time} selected={start === time} onClick={() => handleStartChange(time)}>
            {time}
          </Chip>
        ))}
      </div>

      <div className="flex flex-wrap items-end gap-2">
        <div className="flex flex-col gap-1">
          <Label htmlFor={startId} className="text-xs text-muted-foreground">
            {m.creator_slot_start_label()}
          </Label>
          <Input
            id={startId}
            type="time"
            value={start}
            onChange={(e) => handleStartChange(e.target.value)}
            onKeyDown={handleKeyDown}
            className="h-9 w-[7.5rem] tabular-nums"
          />
        </div>
        <fieldset className="flex flex-col gap-1">
          <legend className="text-xs text-muted-foreground">{m.creator_duration_label()}</legend>
          <div className="flex flex-wrap gap-1.5">
            {DURATIONS.map((option) => (
              <Chip
                key={option.id}
                selected={duration === option.id}
                onClick={() => setDuration(option.id)}
              >
                {option.label()}
              </Chip>
            ))}
          </div>
        </fieldset>

        {duration === 'custom' && (
          <div className="flex flex-col gap-1">
            <Label htmlFor={endId} className="text-xs text-muted-foreground">
              {m.creator_slot_end_label()}
            </Label>
            <Input
              id={endId}
              type="time"
              value={end}
              min={start || undefined}
              onChange={(e) => setEnd(e.target.value)}
              onKeyDown={handleKeyDown}
              className="h-9 w-[7.5rem] tabular-nums"
            />
          </div>
        )}

        {showCapacity && (
          <div className="flex flex-col gap-1">
            <Label htmlFor={capacityId} className="text-xs text-muted-foreground">
              {m.creator_capacity_label()}
            </Label>
            <CapacityField id={capacityId} size="sm" value={capacity} onChange={setCapacity} />
          </div>
        )}

        <Button type="button" variant="outline" size="sm" disabled={!start} onClick={add}>
          <Plus aria-hidden="true" />
          {m.creator_slot_add()}
        </Button>

        {start && (
          <span className="text-xs tabular-nums text-muted-foreground">
            {computedEnd ? `${start} – ${computedEnd}` : start}
          </span>
        )}

        {onApplyToAll && slots.length > 0 && (
          <Button type="button" variant="ghost" size="sm" onClick={onApplyToAll}>
            <CopyPlus aria-hidden="true" />
            {m.creator_apply_to_all()}
          </Button>
        )}
      </div>
    </div>
  )
}
