import { useMemo, useState } from 'react'
import { Badge } from '#/components/ui/badge'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '#/components/ui/tabs'
import { getLocale, intlLocale, m } from '#/lib/i18n'
import type { BookingView } from '#/server/bookings/viewmodel'

type Formatters = { when: Intl.DateTimeFormat; end: Intl.DateTimeFormat }

function Row({
  booking,
  formatters,
  now,
  onCancel,
}: {
  booking: BookingView
  formatters: Formatters
  now: string
  onCancel: (booking: BookingView) => void
}) {
  // Compared against the loader's clock rather than `Date.now()`, so the row renders the same on
  // the server as it does in the browser.
  const cancellable = booking.status === 'confirmed' && booking.startAt > now

  return (
    <tr className="border-b border-border last:border-b-0">
      <td className="py-3 pr-3 align-top text-sm tabular-nums whitespace-nowrap">
        {formatters.when.format(new Date(booking.startAt))}
        <span className="text-muted-foreground">
          {' – '}
          {formatters.end.format(new Date(booking.endAt))}
        </span>
      </td>
      <td className="py-3 pr-3 align-top text-sm">
        <span className="block font-medium">{booking.visitorName}</span>
        <a
          href={`mailto:${booking.visitorEmail}`}
          className="focus-ring block truncate rounded-sm text-muted-foreground hover:underline"
        >
          {booking.visitorEmail}
        </a>
      </td>
      <td className="hidden max-w-[16rem] py-3 pr-3 align-top text-sm text-pretty text-muted-foreground md:table-cell">
        {booking.visitorNote}
      </td>
      <td className="py-3 pr-3 align-top">
        <Badge
          variant={booking.status === 'confirmed' ? 'yes' : 'secondary'}
          className="whitespace-nowrap"
        >
          {booking.status === 'confirmed'
            ? m.bookings_status_confirmed()
            : m.bookings_status_cancelled()}
        </Badge>
      </td>
      <td className="py-3 text-right align-top">
        {cancellable && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            aria-label={m.bookings_cancel_for({ name: booking.visitorName })}
            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={() => onCancel(booking)}
          >
            {m.bookings_cancel()}
          </Button>
        )}
      </td>
    </tr>
  )
}

function Table({
  bookings,
  formatters,
  now,
  emptyText,
  onCancel,
}: {
  bookings: BookingView[]
  formatters: Formatters
  now: string
  emptyText: string
  onCancel: (booking: BookingView) => void
}) {
  if (bookings.length === 0) {
    return (
      <p className="rounded-xl border border-dashed border-border px-6 py-10 text-center text-sm text-balance text-muted-foreground">
        {emptyText}
      </p>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[34rem] border-collapse text-left">
        <thead>
          <tr className="border-b border-border text-xs font-medium tracking-wide text-muted-foreground uppercase">
            <th scope="col" className="py-2 pr-3 font-medium">
              {m.bookings_col_when()}
            </th>
            <th scope="col" className="py-2 pr-3 font-medium">
              {m.bookings_col_who()}
            </th>
            <th scope="col" className="hidden py-2 pr-3 font-medium md:table-cell">
              {m.bookings_col_note()}
            </th>
            <th scope="col" className="py-2 pr-3 font-medium">
              {m.bookings_col_status()}
            </th>
            <th scope="col" className="py-2">
              <span className="sr-only">{m.bookings_cancel()}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {bookings.map((booking) => (
            <Row
              key={booking.id}
              booking={booking}
              formatters={formatters}
              now={now}
              onCancel={onCancel}
            />
          ))}
        </tbody>
      </table>
    </div>
  )
}

/**
 * The organiser's view of who booked what. Upcoming and past are split against `now` — supplied
 * by the route loader rather than read here, so the server's render and the browser's agree on
 * which side of the line every booking falls.
 */
export function BookingsTable({
  bookings,
  timezone,
  now,
  onCancel,
}: {
  bookings: BookingView[]
  /** The page's own time zone: an organiser reads their day in the zone they set their hours in. */
  timezone: string
  now: string
  onCancel: (bookingId: string) => Promise<void>
}) {
  const locale = getLocale()
  const [pending, setPending] = useState<BookingView | null>(null)
  const [busy, setBusy] = useState(false)

  const formatters = useMemo<Formatters>(
    () => ({
      when: new Intl.DateTimeFormat(intlLocale(locale), {
        dateStyle: 'medium',
        timeStyle: 'short',
        timeZone: timezone,
        hourCycle: 'h23',
      }),
      end: new Intl.DateTimeFormat(intlLocale(locale), {
        timeStyle: 'short',
        timeZone: timezone,
        hourCycle: 'h23',
      }),
    }),
    [locale, timezone],
  )

  const { upcoming, past } = useMemo(() => {
    const nowMs = new Date(now).getTime()
    const upcoming: BookingView[] = []
    const past: BookingView[] = []
    for (const booking of bookings) {
      if (new Date(booking.startAt).getTime() >= nowMs) upcoming.push(booking)
      else past.push(booking)
    }
    upcoming.sort((a, b) => a.startAt.localeCompare(b.startAt))
    past.sort((a, b) => b.startAt.localeCompare(a.startAt))
    return { upcoming, past }
  }, [bookings, now])

  async function confirmCancel() {
    if (!pending) return
    setBusy(true)
    try {
      await onCancel(pending.id)
      setPending(null)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Tabs defaultValue="upcoming" className="gap-4">
        <TabsList>
          <TabsTrigger value="upcoming">
            {m.bookings_tab_upcoming()}
            {upcoming.length > 0 && (
              <span className="text-muted-foreground tabular-nums">{upcoming.length}</span>
            )}
          </TabsTrigger>
          <TabsTrigger value="past">{m.bookings_tab_past()}</TabsTrigger>
        </TabsList>
        <TabsContent value="upcoming">
          <Table
            bookings={upcoming}
            formatters={formatters}
            now={now}
            emptyText={m.bookings_empty_upcoming()}
            onCancel={setPending}
          />
        </TabsContent>
        <TabsContent value="past">
          <Table
            bookings={past}
            formatters={formatters}
            now={now}
            emptyText={m.bookings_empty_past()}
            onCancel={setPending}
          />
        </TabsContent>
      </Tabs>

      <Dialog open={pending !== null} onOpenChange={(open) => !open && setPending(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{m.bookings_cancel_title()}</DialogTitle>
            <DialogDescription>
              {m.bookings_cancel_body({ name: pending?.visitorName ?? '' })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setPending(null)}>
              {m.bookings_cancel_keep()}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={busy}
              onClick={() => void confirmCancel()}
            >
              {m.bookings_cancel_confirm()}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
