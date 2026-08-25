import { render } from '@react-email/render'
import { eq } from 'drizzle-orm'
import * as React from 'react'
import BookingCancelled from '../../../emails/BookingCancelled'
import BookingConfirmed from '../../../emails/BookingConfirmed'
import BookingOrganiserNotice from '../../../emails/BookingOrganiserNotice'
import BookingReminder from '../../../emails/BookingReminder'
import BookingRescheduled from '../../../emails/BookingRescheduled'
import BookingRescheduledOrganiser from '../../../emails/BookingRescheduledOrganiser'
import BookingSyncFailed from '../../../emails/BookingSyncFailed'
import { buildIcs } from '#/lib/ics'
import { asLocaleOptions } from '#/lib/i18n'
import { formatOptionLabel } from '#/lib/time'
import * as m from '#/paraglide/messages'
import type { Db } from '#/server/db/client'
import { bookingPages, bookings, user, type CancelledBy } from '#/server/db/schema'
import { sendMail } from '#/server/mailer/mailer'

type Rendered = { subject: string; html: string; text: string }

async function renderEmail(subject: string, el: React.ReactElement): Promise<Rendered> {
  const [html, text] = await Promise.all([render(el), render(el, { plainText: true })])
  return { subject, html, text }
}

export async function renderBookingConfirmed(p: {
  visitorName: string
  pageTitle: string
  organiserName: string
  when: string
  location?: string | null
  manageUrl: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_booking_confirmed_subject({ title: p.pageTitle }, t),
    React.createElement(BookingConfirmed, p),
  )
}

export async function renderBookingOrganiserNotice(p: {
  organiserName: string
  pageTitle: string
  visitorName: string
  visitorEmail: string
  visitorNote?: string | null
  when: string
  location?: string | null
  viewUrl: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_booking_organiser_subject({ title: p.pageTitle }, t),
    React.createElement(BookingOrganiserNotice, p),
  )
}

export async function renderBookingCancelled(p: {
  recipientName: string
  pageTitle: string
  when: string
  cancelledBy: 'you' | 'organiser' | 'visitor'
  visitorName?: string
  viewUrl: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_booking_cancelled_subject({ title: p.pageTitle }, t),
    React.createElement(BookingCancelled, p),
  )
}

export async function renderBookingReminder(p: {
  recipientName: string
  pageTitle: string
  when: string
  location?: string | null
  viewUrl: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_booking_reminder_subject({ title: p.pageTitle }, t),
    React.createElement(BookingReminder, p),
  )
}

export async function renderBookingRescheduled(p: {
  visitorName: string
  pageTitle: string
  organiserName: string
  previousWhen: string
  when: string
  location?: string | null
  manageUrl: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_booking_rescheduled_subject({ title: p.pageTitle }, t),
    React.createElement(BookingRescheduled, p),
  )
}

export async function renderBookingRescheduledOrganiser(p: {
  organiserName: string
  pageTitle: string
  visitorName: string
  previousWhen: string
  when: string
  location?: string | null
  viewUrl: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_booking_rescheduled_org_subject({ title: p.pageTitle }, t),
    React.createElement(BookingRescheduledOrganiser, p),
  )
}

/** Best-effort organiser notice that a Google Calendar sync failed — see
 * `sendGoogleSyncFailedNotice`, the caller that actually sends it. */
export async function renderBookingSyncFailed(p: {
  pageTitle: string
  locale: string
}): Promise<Rendered> {
  const t = asLocaleOptions(p.locale)
  return renderEmail(
    m.email_booking_sync_failed_subject({ title: p.pageTitle }, t),
    React.createElement(BookingSyncFailed, p),
  )
}

export type BookingEmailEnv = {
  EMAIL?: SendEmail
  EMAIL_FROM: string
  APP_URL: string
  APP_ENV?: string
}

/** `endAt` is optional so a bare point in time (e.g. a reschedule's *previous* start, whose end
 * was never recorded) can still be formatted — without it the label just omits the "– HH:mm"
 * range tail (see `formatOptionLabel`). */
function bookingWhen(
  startAt: string,
  endAt: string | null,
  locale: string,
  timeZone: string,
): string {
  const label = formatOptionLabel(
    { kind: 'datetime', startAt, endAt, label: null },
    { locale, timeZone },
  )
  return [label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')
}

function bookingIcs(
  bookingId: string,
  page: { title: string; description: string | null; location: string | null },
  startAt: string,
  endAt: string,
  appUrl: string,
): string {
  return buildIcs({
    uid: `${bookingId}@whenweall`,
    title: page.title,
    description: page.description,
    location: page.location,
    url: `${appUrl}/booking/${bookingId}`,
    start: { dateTime: startAt, endDateTime: endAt },
  })
}

export type SendBookingEmailsKind = 'confirmed' | 'cancelled' | 'reminder' | 'rescheduled'

type MailAttachment = { filename: string; content: string; type: string }

/**
 * Sends the visitor + organiser emails for one booking lifecycle event. Always best-effort: never
 * throws (a failure — missing rows, a render error, a `sendMail` failure — is counted in `failed`
 * rather than propagated), so a booking action never fails because its notification did.
 *
 * `manageToken` is the *raw* manage token, known only to whichever caller just minted or verified
 * it (`createBooking`/`rescheduleBooking` return it once; it is never recoverable from
 * `manage_token_hash` afterwards). Pass it for `confirmed`/`rescheduled` so the visitor's email
 * gets a working manage link; omit it for `cancelled`/`reminder` (e.g. a reminder fired later by
 * an alarm that only has the booking id) and the visitor link falls back to the bare booking URL.
 *
 * `previousStartAt` is required for `kind === 'rescheduled'` — it's `rescheduleBooking`'s own
 * return value (the slot's start before the move) and isn't otherwise recoverable once the
 * booking row has been updated to its new time.
 */
export async function sendBookingEmails(
  env: BookingEmailEnv,
  kind: SendBookingEmailsKind,
  bookingId: string,
  opts: { db: Db; mailer?: typeof sendMail; manageToken?: string; previousStartAt?: string },
): Promise<{ sent: number; failed: number }> {
  const mailer = opts.mailer ?? sendMail
  const { db } = opts

  let sent = 0
  let failed = 0
  async function deliver(to: string, rendered: Rendered, attachments?: MailAttachment[]) {
    const ok = await mailer(env, { to, ...rendered, attachments })
    if (ok) sent += 1
    else failed += 1
  }

  try {
    const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, bookingId) })
    if (!booking) return { sent, failed }

    const page = await db.query.bookingPages.findFirst({
      where: eq(bookingPages.id, booking.pageId),
    })
    if (!page) return { sent, failed }

    const owner = await db.query.user.findFirst({ where: eq(user.id, page.ownerId) })
    if (!owner) return { sent, failed }

    const visitorLocale = booking.visitorLocale ?? 'en'
    const organiserLocale = owner.locale ?? 'en'
    const visitorWhen = bookingWhen(
      booking.startAt,
      booking.endAt,
      visitorLocale,
      booking.visitorTimezone,
    )
    const organiserWhen = bookingWhen(
      booking.startAt,
      booking.endAt,
      organiserLocale,
      page.timezone,
    )
    const manageUrl = `${env.APP_URL}/booking/${bookingId}${opts.manageToken ? `?t=${opts.manageToken}` : ''}`
    const dashboardUrl = `${env.APP_URL}/bookings/${page.id}`
    const publicPageUrl = owner.handle
      ? `${env.APP_URL}/book/${owner.handle}/${page.slug}`
      : env.APP_URL

    if (kind === 'confirmed') {
      const ics = bookingIcs(bookingId, page, booking.startAt, booking.endAt, env.APP_URL)
      const attachments: MailAttachment[] = [
        { filename: 'calendar.ics', content: ics, type: 'text/calendar' },
      ]

      await deliver(
        booking.visitorEmail,
        await renderBookingConfirmed({
          visitorName: booking.visitorName,
          pageTitle: page.title,
          organiserName: owner.name,
          when: visitorWhen,
          location: page.location,
          manageUrl,
          locale: visitorLocale,
        }),
        attachments,
      )

      await deliver(
        owner.email,
        await renderBookingOrganiserNotice({
          organiserName: owner.name,
          pageTitle: page.title,
          visitorName: booking.visitorName,
          visitorEmail: booking.visitorEmail,
          visitorNote: booking.visitorNote,
          when: organiserWhen,
          location: page.location,
          viewUrl: dashboardUrl,
          locale: organiserLocale,
        }),
        attachments,
      )

      return { sent, failed }
    }

    if (kind === 'rescheduled') {
      const ics = bookingIcs(bookingId, page, booking.startAt, booking.endAt, env.APP_URL)
      const attachments: MailAttachment[] = [
        { filename: 'calendar.ics', content: ics, type: 'text/calendar' },
      ]
      const previousVisitorWhen = opts.previousStartAt
        ? bookingWhen(opts.previousStartAt, null, visitorLocale, booking.visitorTimezone)
        : visitorWhen

      await deliver(
        booking.visitorEmail,
        await renderBookingRescheduled({
          visitorName: booking.visitorName,
          pageTitle: page.title,
          organiserName: owner.name,
          previousWhen: previousVisitorWhen,
          when: visitorWhen,
          location: page.location,
          manageUrl,
          locale: visitorLocale,
        }),
        attachments,
      )

      const previousOrganiserWhen = opts.previousStartAt
        ? bookingWhen(opts.previousStartAt, null, organiserLocale, page.timezone)
        : organiserWhen

      // The organiser gets its own "booking moved" template rather than the plain "new booking"
      // notice (`BookingOrganiserNotice`, reused for `confirmed`) — a reschedule is not a new
      // booking, and the organiser needs the previous time alongside the new one.
      await deliver(
        owner.email,
        await renderBookingRescheduledOrganiser({
          organiserName: owner.name,
          pageTitle: page.title,
          visitorName: booking.visitorName,
          previousWhen: previousOrganiserWhen,
          when: organiserWhen,
          location: page.location,
          viewUrl: dashboardUrl,
          locale: organiserLocale,
        }),
        attachments,
      )

      return { sent, failed }
    }

    if (kind === 'cancelled') {
      const cancelledBy: CancelledBy = booking.cancelledBy ?? 'organiser'

      await deliver(
        booking.visitorEmail,
        await renderBookingCancelled({
          recipientName: booking.visitorName,
          pageTitle: page.title,
          when: visitorWhen,
          cancelledBy: cancelledBy === 'visitor' ? 'you' : 'organiser',
          viewUrl: publicPageUrl,
          locale: visitorLocale,
        }),
      )

      await deliver(
        owner.email,
        await renderBookingCancelled({
          recipientName: owner.name,
          pageTitle: page.title,
          when: organiserWhen,
          cancelledBy: cancelledBy === 'organiser' ? 'you' : 'visitor',
          visitorName: booking.visitorName,
          viewUrl: dashboardUrl,
          locale: organiserLocale,
        }),
      )

      return { sent, failed }
    }

    // reminder
    await deliver(
      booking.visitorEmail,
      await renderBookingReminder({
        recipientName: booking.visitorName,
        pageTitle: page.title,
        when: visitorWhen,
        location: page.location,
        viewUrl: manageUrl,
        locale: visitorLocale,
      }),
    )

    await deliver(
      owner.email,
      await renderBookingReminder({
        recipientName: owner.name,
        pageTitle: page.title,
        when: organiserWhen,
        location: page.location,
        viewUrl: dashboardUrl,
        locale: organiserLocale,
      }),
    )

    return { sent, failed }
  } catch (err) {
    console.error('[booking-emails] failed to send', err)
    return { sent, failed: failed + 1 }
  }
}

/**
 * Best-effort organiser notice for a failed Google Calendar sync (create/reschedule/cancel — see
 * the `syncGoogle*` helpers in `bookings.functions.ts`). Sent at most once per operation: the
 * caller invokes this from its single `catch` block, and this function makes no retry attempt of
 * its own — a failure to *send the notice* is swallowed the same way `sendBookingEmails` swallows
 * one, since a notification about a sync failure must not itself become a hard failure.
 */
export async function sendGoogleSyncFailedNotice(
  env: BookingEmailEnv,
  bookingId: string,
  opts: { db: Db; mailer?: typeof sendMail },
): Promise<void> {
  const mailer = opts.mailer ?? sendMail
  const { db } = opts

  try {
    const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, bookingId) })
    if (!booking) return

    const page = await db.query.bookingPages.findFirst({
      where: eq(bookingPages.id, booking.pageId),
    })
    if (!page) return

    const owner = await db.query.user.findFirst({ where: eq(user.id, page.ownerId) })
    if (!owner) return

    const rendered = await renderBookingSyncFailed({
      pageTitle: page.title,
      locale: owner.locale ?? 'en',
    })
    await mailer(env, { to: owner.email, ...rendered })
  } catch (err) {
    console.error('[booking-emails] failed to send Google sync-failed notice', err)
  }
}
