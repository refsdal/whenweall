import { useId, useState, type ChangeEvent } from 'react'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Switch } from '#/components/ui/switch'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

/** Mirrors `capacitySchema` in `server/polls/schemas.ts`. */
const MIN = 1
const MAX = 10000

/**
 * A slot's capacity: a number of spots, or "unlimited". Used for a signup poll's per-day,
 * per-time-slot and per-text-option capacity, so it stays deliberately small and reusable at two
 * sizes — a full labelled field in the settings-y spots, a compact one inline in a slot row.
 */
export function CapacityField({
  value,
  onChange,
  id,
  size = 'md',
}: {
  value: number | null
  onChange: (value: number | null) => void
  id?: string
  size?: 'sm' | 'md'
}) {
  const autoId = useId()
  const inputId = id ?? autoId
  const unlimitedId = `${inputId}-unlimited`
  const hintId = `${inputId}-hint`
  const unlimited = value === null

  // Remembers the last real number so switching "unlimited" off hands back something usable
  // instead of always resetting to 1.
  const [lastValue, setLastValue] = useState(value ?? MIN)
  const [syncedValue, setSyncedValue] = useState(value)
  const [text, setText] = useState(value === null ? '' : String(value))
  // Adjusting state during render (rather than in an effect) so the field's own draft text stays
  // in sync the moment `value` changes from outside — e.g. the "unlimited" switch — without an
  // extra render caused by a `setState` inside `useEffect`.
  if (value !== syncedValue) {
    setSyncedValue(value)
    setText(value === null ? '' : String(value))
    if (value !== null) setLastValue(value)
  }

  function handleChange(event: ChangeEvent<HTMLInputElement>) {
    const raw = event.target.value
    setText(raw)
    if (raw === '') return
    const parsed = Number(raw)
    if (!Number.isInteger(parsed) || parsed < MIN || parsed > MAX) return
    onChange(parsed)
  }

  function handleUnlimitedChange(next: boolean) {
    onChange(next ? null : lastValue)
  }

  return (
    <div className={cn('flex items-center', size === 'sm' ? 'gap-1.5' : 'gap-2.5')}>
      <div className="flex flex-col gap-1">
        {size === 'md' && (
          <Label htmlFor={inputId} className="text-xs text-muted-foreground">
            {m.creator_capacity_label()}
          </Label>
        )}
        <Input
          id={inputId}
          type="number"
          inputMode="numeric"
          min={MIN}
          max={MAX}
          step={1}
          disabled={unlimited}
          value={unlimited ? '' : text}
          placeholder={unlimited ? '∞' : undefined}
          aria-label={size === 'sm' ? m.creator_capacity_label() : undefined}
          aria-describedby={hintId}
          onChange={handleChange}
          className={cn(
            'tabular-nums',
            size === 'sm' ? 'h-8 w-16 px-2 text-sm' : 'h-9 w-20',
            unlimited && 'text-center',
          )}
        />
      </div>

      <div className="flex items-center gap-1.5">
        <Switch
          id={unlimitedId}
          size={size === 'sm' ? 'sm' : 'default'}
          checked={unlimited}
          onCheckedChange={handleUnlimitedChange}
        />
        <Label
          htmlFor={unlimitedId}
          className="text-xs font-normal text-muted-foreground select-none"
        >
          {m.creator_capacity_unlimited()}
        </Label>
      </div>

      <span id={hintId} className="sr-only">
        {m.creator_capacity_hint()}
      </span>
    </div>
  )
}
