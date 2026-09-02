import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { SlotPicker } from '#/components/booking/SlotPicker'
import { slotSummary } from '#/components/booking/BookingForm'
import type { Interval } from '#/lib/availability'
import { getLocale, m } from '#/lib/i18n'
import { getPublicAvailability } from '#/api/bookings'
import type { PublicPageView } from '#/api/types'

/** `YYYY-MM` for an instant, read in the viewer's zone. */
function monthKeyIn(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit' })
    .format(new Date(iso))
    .slice(0, 7)
}

/** A month plus a day of padding either side — see the same helper on the public route. */
function monthWindow(month: string): { from: string; to: string } {
  const [y, mo] = month.split('-').map(Number) as [number, number]
  return {
    from: new Date(Date.UTC(y, mo - 1, 1) - 86_400_000).toISOString().slice(0, 10),
    to: new Date(Date.UTC(y, mo, 0) + 86_400_000).toISOString().slice(0, 10),
  }
}

/**
 * Moving a booking is the same choice as making one, so it is the same picker — only the slots
 * are fetched on the client here, because a dialog has no loader of its own.
 */
export function RescheduleDialog({
  open,
  onOpenChange,
  handle,
  slug,
  timeZone,
  now,
  submitting = false,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  handle: string
  slug: string
  timeZone: string
  now: string
  submitting?: boolean
  onConfirm: (startAt: string) => void | Promise<void>
}) {
  const locale = getLocale()
  const [month, setMonth] = useState(() => monthKeyIn(now, timeZone))
  const [picked, setPicked] = useState<Interval | null>(null)
  // One piece of state, stamped with the month it describes: "loading" is then simply "what we
  // have isn't the month being shown", with no second setState to clear it (which an effect body
  // isn't allowed to do anyway).
  const [loaded, setLoaded] = useState<{
    month: string
    page: PublicPageView | null
    slots: Interval[]
  } | null>(null)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    void getPublicAvailability({ handle, slug, ...monthWindow(month) })
      .then((result) => {
        if (cancelled) return
        setLoaded({ month, page: result?.page ?? null, slots: result?.slots ?? [] })
      })
      .catch(() => {
        if (!cancelled) setLoaded({ month, page: null, slots: [] })
      })
    return () => {
      cancelled = true
    }
  }, [handle, month, open, slug, timeZone])

  const slots = loaded && loaded.month === month ? loaded.slots : null
  const page = loaded?.page ?? null

  const handleOpenChange = useCallback(
    (next: boolean) => {
      if (!next) setPicked(null)
      onOpenChange(next)
    },
    [onOpenChange],
  )

  const maxMonth = page
    ? monthKeyIn(
        new Date(new Date(now).getTime() + page.maxDaysAhead * 86_400_000).toISOString(),
        timeZone,
      )
    : undefined

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-h-[92dvh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{m.booking_manage_reschedule_title()}</DialogTitle>
          <DialogDescription>{m.booking_manage_reschedule_body()}</DialogDescription>
        </DialogHeader>

        {slots === null ? (
          <p className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
            <Loader2 aria-hidden="true" className="size-4 animate-spin" />
            {m.book_public_loading()}
          </p>
        ) : (
          <SlotPicker
            slots={slots}
            timeZone={timeZone}
            month={month}
            onMonthChange={setMonth}
            minMonth={monthKeyIn(now, timeZone)}
            maxMonth={maxMonth}
            selectedStart={picked?.start ?? null}
            onPick={setPicked}
          />
        )}

        <DialogFooter className="sm:items-center">
          {picked && (
            <p
              className="mr-auto text-sm font-medium first-letter:uppercase"
              suppressHydrationWarning
            >
              {slotSummary(picked, timeZone, locale)}
            </p>
          )}
          <Button
            type="button"
            variant="ghost"
            onClick={() => handleOpenChange(false)}
            disabled={submitting}
          >
            {m.common_cancel()}
          </Button>
          <Button
            type="button"
            disabled={!picked || submitting}
            onClick={() => picked && void onConfirm(picked.start)}
          >
            {submitting ? m.booking_manage_rescheduling() : m.booking_manage_reschedule_confirm()}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
