import { useState, useSyncExternalStore } from 'react'
import { Globe } from 'lucide-react'
import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '#/components/ui/popover'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

export const TZ_STORAGE_KEY = 'whenweall:tz'

/** The visitor's own zone, or null when the browser won't say. */
export function browserTimeZone(): string | null {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || null
  } catch {
    return null
  }
}

export function loadStoredTimeZone(): string | null {
  if (typeof window === 'undefined') return null
  try {
    return window.localStorage.getItem(TZ_STORAGE_KEY)
  } catch {
    return null
  }
}

const listeners = new Set<() => void>()

export function storeTimeZone(zone: string): void {
  try {
    window.localStorage.setItem(TZ_STORAGE_KEY, zone)
  } catch {
    // Blocked storage: the choice still applies for this page view.
  }
  for (const listener of listeners) listener()
}

function subscribeTimeZone(onChange: () => void): () => void {
  listeners.add(onChange)
  if (typeof window !== 'undefined') window.addEventListener('storage', onChange)
  return () => {
    listeners.delete(onChange)
    if (typeof window !== 'undefined') window.removeEventListener('storage', onChange)
  }
}

/**
 * The zone the grid is drawn in: the visitor's stored choice, else their browser's zone, else the
 * organiser's. Exposed as an external store because neither of the first two exist on the server —
 * SSR renders the organiser's zone and the browser swaps in its own right after hydration.
 */
export function useViewerTimeZone(pollTimeZone: string): string {
  return useSyncExternalStore(
    subscribeTimeZone,
    () => loadStoredTimeZone() ?? browserTimeZone() ?? pollTimeZone,
    () => pollTimeZone,
  )
}

/** Every zone the browser knows. Computed once: the list is large and never changes. */
let cachedZones: string[] | null = null
const NO_ZONES: string[] = []
const noopSubscribe = () => () => {}

function zoneList(): string[] {
  if (cachedZones) return cachedZones
  const supported = (
    Intl as unknown as { supportedValuesOf?: (key: string) => string[] }
  ).supportedValuesOf?.('timeZone')
  cachedZones = supported && supported.length > 0 ? supported : ['UTC']
  return cachedZones
}

/**
 * "Times shown in Europe/Oslo · change".
 *
 * The zone list comes from `Intl.supportedValuesOf`, which is only read after mount: the server
 * and the browser don't agree on it, and a mismatched `<option>` list would break hydration.
 */
export function TimezoneSwitch({
  value,
  onChange,
  pollTimeZone,
  className,
}: {
  value: string
  onChange: (zone: string) => void
  pollTimeZone: string
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const zones = useSyncExternalStore(noopSubscribe, zoneList, () => NO_ZONES)

  return (
    <div className={cn('flex items-center gap-1 text-xs text-muted-foreground', className)}>
      <Globe aria-hidden="true" className="size-3.5 shrink-0" />
      <span suppressHydrationWarning>{m.poll_timezone_label({ zone: value })}</span>
      <span aria-hidden="true">·</span>
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger className="focus-ring cursor-pointer rounded-sm font-medium text-primary-ink underline underline-offset-2">
          {m.poll_timezone_change()}
        </PopoverTrigger>
        <PopoverContent align="start" className="w-72">
          <PopoverHeader>
            <PopoverTitle>
              <label htmlFor="poll-timezone">{m.poll_timezone_select_label()}</label>
            </PopoverTitle>
          </PopoverHeader>
          <select
            id="poll-timezone"
            value={value}
            onChange={(event) => onChange(event.target.value)}
            className="focus-ring mt-2 h-10 w-full rounded-md border border-input bg-transparent px-2 text-sm"
          >
            {!zones.includes(value) && <option value={value}>{value}</option>}
            {zones.map((zone) => (
              <option key={zone} value={zone}>
                {zone}
              </option>
            ))}
          </select>
          {value !== pollTimeZone && (
            <button
              type="button"
              onClick={() => onChange(pollTimeZone)}
              className="focus-ring mt-3 cursor-pointer rounded-sm text-xs text-primary-ink underline underline-offset-2"
            >
              {m.poll_timezone_reset({ zone: pollTimeZone })}
            </button>
          )}
        </PopoverContent>
      </Popover>
    </div>
  )
}
