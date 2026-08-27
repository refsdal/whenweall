import { Link } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { CalendarPlus, Check, MapPin } from 'lucide-react'
import { Button } from '#/components/ui/button'
import type { Interval } from '#/lib/availability'
import { getLocale, m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'
import { slotSummary } from '#/components/booking/BookingForm'

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

function utcStamp(iso: string): string {
  const date = new Date(iso)
  return (
    `${date.getUTCFullYear()}${pad(date.getUTCMonth() + 1)}${pad(date.getUTCDate())}` +
    `T${pad(date.getUTCHours())}${pad(date.getUTCMinutes())}00Z`
  )
}

/** "Add to Google Calendar" for a booked slot — same template URL the poll banner uses. */
export function googleCalendarUrl(input: {
  title: string
  location: string | null
  description?: string | null
  slot: Interval
}): string {
  const params = new URLSearchParams({
    action: 'TEMPLATE',
    text: input.title,
    dates: `${utcStamp(input.slot.start)}/${utcStamp(input.slot.end)}`,
  })
  if (input.location) params.set('location', input.location)
  if (input.description) params.set('details', input.description)
  return `https://calendar.google.com/calendar/render?${params.toString()}`
}

/**
 * The payoff: the time is held, here is how to put it in your calendar, and here is the link
 * that lets you move or cancel it later.
 */
export function BookingConfirmed({
  bookingId,
  manageToken,
  title,
  location,
  description,
  slot,
  timeZone,
  email,
  onBookAnother,
}: {
  bookingId: string
  manageToken: string
  title: string
  location: string | null
  description?: string | null
  slot: Interval
  timeZone: string
  email: string
  onBookAnother: () => void
}) {
  const locale = getLocale()
  const reduceMotion = useReducedMotion()

  return (
    <motion.section
      data-testid="booking-confirmed"
      initial={reduceMotion ? false : { opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      transition={spring}
      className="surface flex flex-col gap-4 border-[var(--yes)]/40 bg-yes-soft/40 p-5"
    >
      <div className="flex items-start gap-3">
        {/* The tick lands a beat after the card, with a ring going out from under it — the visual
            half of the confirmation the confetti is celebrating. */}
        <span className="relative mt-0.5 inline-flex size-9 shrink-0 items-center justify-center">
          {!reduceMotion && (
            <motion.span
              aria-hidden="true"
              className="absolute inset-0 rounded-full bg-yes-soft"
              initial={{ scale: 1, opacity: 0.8 }}
              animate={{ scale: 1.9, opacity: 0 }}
              transition={{ duration: 0.7, ease: 'easeOut', delay: 0.1 }}
            />
          )}
          <motion.span
            className="relative inline-flex size-9 items-center justify-center rounded-full bg-yes-soft text-yes-ink"
            initial={reduceMotion ? false : { scale: 0.4 }}
            animate={{ scale: 1 }}
            transition={{ ...spring, delay: 0.1 }}
          >
            <Check aria-hidden="true" className="size-4.5" strokeWidth={3} />
          </motion.span>
        </span>
        <div className="min-w-0">
          <h2 className="display text-xl">{m.book_confirmed_title()}</h2>
          <p className="mt-0.5 text-sm font-medium first-letter:uppercase" suppressHydrationWarning>
            {slotSummary(slot, timeZone, locale)}
          </p>
          {location && (
            <p className="mt-0.5 flex items-center gap-1.5 text-sm text-muted-foreground">
              <MapPin aria-hidden="true" className="size-3.5 shrink-0" />
              {location}
            </p>
          )}
          <p className="mt-2 text-sm text-muted-foreground">{m.book_confirmed_body({ email })}</p>
        </div>
      </div>

      <div className="flex flex-wrap gap-2">
        <Button asChild variant="outline" size="sm">
          <a
            href={`/booking/${bookingId}/calendar.ics?t=${encodeURIComponent(manageToken)}`}
            download
          >
            <CalendarPlus aria-hidden="true" />
            {m.poll_add_to_calendar()}
          </a>
        </Button>
        <Button asChild variant="ghost" size="sm">
          <a
            href={googleCalendarUrl({ title, location, description, slot })}
            target="_blank"
            rel="noreferrer noopener"
          >
            {m.poll_add_to_google()}
          </a>
        </Button>
        <Button asChild variant="ghost" size="sm">
          <Link to="/booking/$id" params={{ id: bookingId }} search={{ t: manageToken }}>
            {m.book_confirmed_manage()}
          </Link>
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onBookAnother}>
          {m.book_confirmed_another()}
        </Button>
      </div>
    </motion.section>
  )
}
