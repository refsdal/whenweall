import { useMemo, useSyncExternalStore } from 'react'
import { Sparkles } from 'lucide-react'
import { generateSlots } from '#/lib/availability'
import { getLocale, intlLocale, m } from '#/lib/i18n'
import { draftRules, type EditorDraft } from '#/components/booking/editor-state'

const PREVIEW_DAYS = 7
const MAX_SHOWN = 8

const noopSubscribe = () => () => {}

/**
 * "Next available slots": the same pure generator the public page runs, fed straight from the
 * draft. No server call, so the preview reacts to every keystroke in the availability grid.
 *
 * "Now" is only read after mount — during SSR there is no meaningful clock to render against, and
 * a server-rendered list of times would never match what the browser computes a moment later.
 */
export function SlotPreview({ draft }: { draft: EditorDraft }) {
  const locale = getLocale()
  // The clock is a browser-only fact: the server snapshot is `false`, so the first client render
  // matches the SSR output exactly and the real times appear on the render right after hydration.
  const mounted = useSyncExternalStore(
    noopSubscribe,
    () => true,
    () => false,
  )
  const now = useMemo(() => (mounted ? new Date() : null), [mounted])

  const slots = useMemo(() => {
    if (!now) return []
    return generateSlots(draftRules(draft), {
      from: now,
      to: new Date(now.getTime() + PREVIEW_DAYS * 86_400_000),
      now,
      busy: [],
    })
  }, [draft, now])

  const format = useMemo(
    () =>
      new Intl.DateTimeFormat(intlLocale(locale), {
        weekday: 'short',
        day: 'numeric',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
        hourCycle: 'h23',
        timeZone: draft.timezone,
      }),
    [locale, draft.timezone],
  )

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-1.5">
        <Sparkles aria-hidden="true" className="size-3.5 text-muted-foreground" />
        <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {m.booking_preview_title()}
        </h3>
      </div>

      {now === null ? (
        <p className="text-sm text-muted-foreground">{m.booking_preview_hint()}</p>
      ) : slots.length === 0 ? (
        <p className="text-sm text-muted-foreground">{m.booking_preview_empty()}</p>
      ) : (
        <>
          <ul className="flex flex-wrap gap-1.5">
            {slots.slice(0, MAX_SHOWN).map((slot) => (
              <li
                key={slot.start}
                className="rounded-full bg-accent-soft px-2.5 py-1 text-xs font-medium text-accent-foreground tabular-nums"
              >
                {format.format(new Date(slot.start))}
              </li>
            ))}
            {slots.length > MAX_SHOWN && (
              <li className="rounded-full bg-muted px-2.5 py-1 text-xs font-medium text-muted-foreground">
                {m.booking_preview_more({ count: slots.length - MAX_SHOWN })}
              </li>
            )}
          </ul>
          <p className="text-sm text-muted-foreground">{m.booking_preview_hint()}</p>
        </>
      )}
    </div>
  )
}
