import { useCallback, useMemo, useState } from 'react'
import { useServerFn } from '@tanstack/react-start'
import { CalendarClock, Clock, MapPin, PauseCircle, User } from 'lucide-react'
import { toast } from 'sonner'
import { BookingConfirmed } from '#/components/booking/BookingConfirmed'
import { BookingForm, type BookingFormValues } from '#/components/booking/BookingForm'
import { SlotPicker } from '#/components/booking/SlotPicker'
import { TimezoneSwitch } from '#/components/poll/TimezoneSwitch'
import type { Interval } from '#/lib/availability'
import { saveBookingToken } from '#/lib/booking-tokens'
import { celebrate } from '#/lib/confetti'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { bookSlot } from '#/server/bookings/bookings.functions'
import type { PublicPageView } from '#/server/bookings/viewmodel'

/** `YYYY-MM` for an instant, read in the visitor's zone. */
function monthKeyIn(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit' })
    .format(new Date(iso))
    .slice(0, 7)
}

/** Maps a booking failure to the sentence that actually helps the visitor. */
function messageForError(error: unknown): string {
  switch (errorCode(error)) {
    case 'SLOT_UNAVAILABLE':
      return m.book_error_slot_unavailable()
    case 'PAGE_PAUSED':
      return m.book_error_paused()
    case 'BOOKING_PAST':
      return m.book_error_past()
    case 'CAPTCHA_FAILED':
      return m.poll_error_captcha()
    case 'RATE_LIMITED':
      return m.error_rate_limited()
    default:
      return m.book_error_generic()
  }
}

type Confirmation = { bookingId: string; manageToken: string; slot: Interval; email: string }

/**
 * What a visitor sees at `/book/<handle>/<slug>`: who they are booking with on the left, the
 * open days and times on the right, and — once they pick one — a short form and a confirmation.
 *
 * Everything time-shaped is rendered in `timeZone`, which the route keeps in the URL: the server
 * and the browser therefore agree on every label, and switching zones is a navigation the loader
 * answers with slots for the same window.
 */
export function PublicBookingPage({
  page,
  slots,
  now,
  month,
  timeZone,
  onMonthChange,
  onTimeZoneChange,
  onBooked,
}: {
  page: PublicPageView
  slots: Interval[]
  now: string
  month: string
  timeZone: string
  onMonthChange: (month: string) => void
  onTimeZoneChange: (zone: string) => void
  onBooked: () => void | Promise<void>
}) {
  const book = useServerFn(bookSlot)
  // The chosen slot outlives the dialog: closing it and picking another time keeps whatever the
  // visitor already typed, because the form stays mounted.
  const [picked, setPicked] = useState<Interval | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [confirmed, setConfirmed] = useState<Confirmation | null>(null)

  const minMonth = useMemo(() => monthKeyIn(now, timeZone), [now, timeZone])
  const maxMonth = useMemo(
    () =>
      monthKeyIn(
        new Date(new Date(now).getTime() + page.maxDaysAhead * 86_400_000).toISOString(),
        timeZone,
      ),
    [now, page.maxDaysAhead, timeZone],
  )

  const handleSubmit = useCallback(
    async (values: BookingFormValues) => {
      setSubmitting(true)
      try {
        const result = await book({
          data: {
            pageId: page.id,
            startAt: values.startAt,
            name: values.name,
            email: values.email,
            note: values.note,
            timezone: timeZone,
            turnstileToken: values.turnstileToken,
          },
        })
        saveBookingToken(result.bookingId, result.manageToken)
        celebrate('vote')
        const endAt = new Date(
          new Date(values.startAt).getTime() + page.slotDurationMin * 60_000,
        ).toISOString()
        setConfirmed({
          bookingId: result.bookingId,
          manageToken: result.manageToken,
          slot: { start: values.startAt, end: endAt },
          email: values.email,
        })
        setFormOpen(false)
        setPicked(null)
        await onBooked()
      } catch (error) {
        toast.error(messageForError(error))
        // These three all mean "this particular time is no longer bookable": close the form so
        // the visitor is looking at the list again rather than at a slot that is already gone.
        const code = errorCode(error)
        if (code === 'SLOT_UNAVAILABLE' || code === 'BOOKING_PAST' || code === 'PAGE_PAUSED') {
          setFormOpen(false)
        }
        // Whatever went wrong, the slot list is the thing that may be stale — refetch it so the
        // visitor is choosing from what is actually still free.
        await onBooked()
      } finally {
        setSubmitting(false)
      }
    },
    [book, onBooked, page.id, page.slotDurationMin, timeZone],
  )

  const paused = page.status === 'paused'

  return (
    <div
      data-testid="booking-page"
      className="mx-auto flex w-full max-w-5xl flex-col gap-8 px-5 py-8 sm:py-12 lg:flex-row lg:gap-12"
    >
      <aside className="flex w-full flex-col gap-3 lg:w-72 lg:shrink-0">
        <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
          <User aria-hidden="true" className="size-3.5 shrink-0" />
          {m.book_public_organiser({ name: page.owner.name })}
        </p>
        <h1 className="display text-2xl text-balance sm:text-3xl">{page.title}</h1>

        <ul className="flex flex-col gap-1.5 text-sm text-muted-foreground">
          <li className="flex items-center gap-2">
            <Clock aria-hidden="true" className="size-4 shrink-0" />
            {m.book_public_duration({ count: page.slotDurationMin })}
          </li>
          {page.location && (
            <li className="flex items-start gap-2">
              <MapPin aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
              <span className="break-words">{page.location}</span>
            </li>
          )}
        </ul>

        {page.description && (
          <p className="text-sm text-pretty whitespace-pre-line text-muted-foreground">
            {page.description}
          </p>
        )}

        <TimezoneSwitch
          value={timeZone}
          onChange={onTimeZoneChange}
          pollTimeZone={page.timezone}
          className="mt-1"
        />
      </aside>

      <div className="flex min-w-0 flex-1 flex-col gap-6">
        {confirmed ? (
          <BookingConfirmed
            bookingId={confirmed.bookingId}
            manageToken={confirmed.manageToken}
            title={page.title}
            location={page.location}
            description={page.description}
            slot={confirmed.slot}
            timeZone={timeZone}
            email={confirmed.email}
            onBookAnother={() => setConfirmed(null)}
          />
        ) : paused ? (
          <section
            data-testid="booking-paused"
            className="surface flex flex-col items-center gap-2 px-6 py-12 text-center"
          >
            <PauseCircle aria-hidden="true" className="size-6 text-muted-foreground" />
            <h2 className="display text-xl">{m.book_public_paused_title()}</h2>
            <p className="max-w-sm text-sm text-balance text-muted-foreground">
              {m.book_public_paused_body({ name: page.owner.name })}
            </p>
          </section>
        ) : (
          <>
            <h2 className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
              <CalendarClock aria-hidden="true" className="size-4" />
              {m.book_public_pick_time()}
            </h2>
            <SlotPicker
              slots={slots}
              timeZone={timeZone}
              month={month}
              onMonthChange={onMonthChange}
              minMonth={minMonth}
              maxMonth={maxMonth}
              selectedStart={formOpen ? (picked?.start ?? null) : null}
              onPick={(slot) => {
                setPicked(slot)
                setFormOpen(true)
              }}
            />
          </>
        )}
      </div>

      {picked && (
        <BookingForm
          open={formOpen}
          onOpenChange={setFormOpen}
          title={page.title}
          location={page.location}
          slot={picked}
          timeZone={timeZone}
          submitting={submitting}
          onSubmit={handleSubmit}
        />
      )}
    </div>
  )
}
