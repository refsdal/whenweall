import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useServerFn } from '@tanstack/react-start'
import { CalendarPlus, Clock, MapPin, StickyNote, User } from 'lucide-react'
import { toast } from 'sonner'
import { slotSummary } from '#/components/booking/BookingForm'
import { googleCalendarUrl } from '#/components/booking/BookingConfirmed'
import { RescheduleDialog } from '#/components/booking/RescheduleDialog'
import { storeTimeZone, TimezoneSwitch, useViewerTimeZone } from '#/components/poll/TimezoneSwitch'
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
import { errorCode } from '#/lib/errors'
import { getLocale, m } from '#/lib/i18n'
import { cancelBooking, rescheduleBooking } from '#/server/bookings/bookings.functions'
import type { BookingForManage } from '#/server/bookings/viewmodel'

function messageForError(error: unknown): string {
  switch (errorCode(error)) {
    case 'SLOT_UNAVAILABLE':
      return m.book_error_slot_unavailable()
    case 'BOOKING_PAST':
      return m.book_error_past()
    case 'PAGE_PAUSED':
      return m.book_error_paused()
    case 'RATE_LIMITED':
      return m.error_rate_limited()
    default:
      return m.booking_manage_error_generic()
  }
}

function DetailRow({
  icon: Icon,
  label,
  children,
}: {
  icon: typeof Clock
  label: string
  children: React.ReactNode
}) {
  return (
    <div className="flex items-start gap-3 py-3">
      <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <dt className="text-xs tracking-wide text-muted-foreground uppercase">{label}</dt>
        <dd className="text-sm break-words">{children}</dd>
      </div>
    </div>
  )
}

/**
 * `/booking/<id>`: everything one visitor (or the organiser) needs to know about a single
 * booking, and the two things they might want to do with it — move it, or call it off.
 *
 * The zone is the one the booking was made in until the visitor says otherwise, and — like every
 * other time-of-day in this app — it is only read from the browser after mount.
 */
export function ManageBooking({
  booking,
  now,
  token,
  onChanged,
}: {
  booking: BookingForManage
  now: string
  token?: string
  onChanged: () => void | Promise<void>
}) {
  const locale = getLocale()
  const cancelFn = useServerFn(cancelBooking)
  const rescheduleFn = useServerFn(rescheduleBooking)

  const storedZone = useViewerTimeZone(booking.visitorTimezone)
  const [chosenZone, setChosenZone] = useState<string | null>(null)
  const timeZone = chosenZone ?? storedZone

  const [confirmOpen, setConfirmOpen] = useState(false)
  const [rescheduleOpen, setRescheduleOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  const cancelled = booking.status === 'cancelled'
  // Compared against the loader's clock, so the page renders the same on both sides of hydration.
  const past = booking.startAt <= now
  const canAct = !cancelled && !past
  const bookAgainTo = booking.page.handle
    ? { handle: booking.page.handle, slug: booking.page.slug }
    : null
  const icsHref = token
    ? `/booking/${booking.id}/calendar.ics?t=${encodeURIComponent(token)}`
    : `/booking/${booking.id}/calendar.ics`

  function handleTimeZone(zone: string) {
    setChosenZone(zone)
    storeTimeZone(zone)
  }

  async function handleCancel() {
    setBusy(true)
    try {
      await cancelFn({ data: { bookingId: booking.id, token } })
      setConfirmOpen(false)
      toast.success(m.booking_manage_cancelled_toast())
      await onChanged()
    } catch (error) {
      toast.error(messageForError(error))
    } finally {
      setBusy(false)
    }
  }

  async function handleReschedule(startAt: string) {
    setBusy(true)
    try {
      await rescheduleFn({ data: { bookingId: booking.id, token, startAt } })
      setRescheduleOpen(false)
      toast.success(m.booking_manage_rescheduled())
      await onChanged()
    } catch (error) {
      toast.error(messageForError(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      data-testid="manage-booking"
      className="mx-auto flex w-full max-w-2xl flex-col gap-5 px-5 py-8 sm:py-12"
    >
      <header className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={cancelled ? 'secondary' : 'yes'}>
            {cancelled ? m.bookings_status_cancelled() : m.bookings_status_confirmed()}
          </Badge>
          {past && !cancelled && (
            <span className="text-xs text-muted-foreground">{m.booking_manage_past()}</span>
          )}
        </div>
        <h1 className="display text-2xl text-balance sm:text-3xl">{booking.page.title}</h1>
        <p className="text-sm text-muted-foreground">
          {m.book_public_organiser({ name: booking.page.owner.name })}
        </p>
      </header>

      {cancelled ? (
        <section className="surface flex flex-col items-start gap-2 p-5">
          <h2 className="display text-lg">{m.booking_manage_cancelled_title()}</h2>
          <p className="text-sm text-muted-foreground">{m.booking_manage_cancelled_body()}</p>
          {bookAgainTo && (
            <Button asChild variant="outline" size="sm" className="mt-1">
              <Link to="/book/$handle/$slug" params={bookAgainTo} search={{}}>
                {m.booking_manage_book_again()}
              </Link>
            </Button>
          )}
        </section>
      ) : null}

      <section className="surface p-5">
        <h2 className="sr-only">{m.booking_manage_title()}</h2>
        <dl className="divide-y divide-border">
          <DetailRow icon={Clock} label={m.booking_manage_when()}>
            <span
              className={cancelled ? 'line-through opacity-70' : 'first-letter:uppercase'}
              suppressHydrationWarning
            >
              {slotSummary({ start: booking.startAt, end: booking.endAt }, timeZone, locale)}
            </span>
          </DetailRow>
          {booking.page.location && (
            <DetailRow icon={MapPin} label={m.booking_manage_where()}>
              {booking.page.location}
            </DetailRow>
          )}
          <DetailRow icon={User} label={m.booking_manage_who()}>
            {booking.visitorName}
            <span className="block text-muted-foreground">{booking.visitorEmail}</span>
          </DetailRow>
          {booking.visitorNote && (
            <DetailRow icon={StickyNote} label={m.booking_manage_note()}>
              <span className="whitespace-pre-line">{booking.visitorNote}</span>
            </DetailRow>
          )}
        </dl>

        <TimezoneSwitch
          value={timeZone}
          onChange={handleTimeZone}
          pollTimeZone={booking.page.timezone}
          className="mt-4"
        />
      </section>

      {!cancelled && (
        <div className="flex flex-wrap gap-2">
          {canAct && (
            <>
              <Button type="button" onClick={() => setRescheduleOpen(true)} disabled={busy}>
                {m.booking_manage_reschedule()}
              </Button>
              <Button
                type="button"
                variant="outline"
                className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                onClick={() => setConfirmOpen(true)}
                disabled={busy}
              >
                {m.booking_manage_cancel()}
              </Button>
            </>
          )}
          <Button asChild variant="ghost">
            <a href={icsHref} download>
              <CalendarPlus aria-hidden="true" />
              {m.poll_add_to_calendar()}
            </a>
          </Button>
          <Button asChild variant="ghost">
            <a
              href={googleCalendarUrl({
                title: booking.page.title,
                location: booking.page.location,
                slot: { start: booking.startAt, end: booking.endAt },
              })}
              target="_blank"
              rel="noreferrer noopener"
            >
              {m.poll_add_to_google()}
            </a>
          </Button>
        </div>
      )}

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{m.booking_manage_cancel_title()}</DialogTitle>
            <DialogDescription>{m.booking_manage_cancel_body()}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setConfirmOpen(false)}
              disabled={busy}
            >
              {m.booking_manage_cancel_keep()}
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={() => void handleCancel()}
              disabled={busy}
            >
              {m.booking_manage_cancel_confirm()}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {booking.page.handle && (
        <RescheduleDialog
          open={rescheduleOpen}
          onOpenChange={setRescheduleOpen}
          handle={booking.page.handle}
          slug={booking.page.slug}
          timeZone={timeZone}
          now={now}
          submitting={busy}
          onConfirm={handleReschedule}
        />
      )}
    </div>
  )
}
