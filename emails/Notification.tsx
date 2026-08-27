import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

/** Lifecycle and booking events that this generic template renders. `poll.closed` is deliberately
 * absent — it already has a dedicated template (`Closed.tsx`) with translated copy, and
 * `renderNotification` delegates to it rather than duplicating the strings. */
export type NotificationTemplateEvent =
  | 'deadline.approaching'
  | 'poll.finalized'
  | 'booking.created'
  | 'booking.cancelled'
  | 'booking.rescheduled'

export type NotificationProps = {
  event: NotificationTemplateEvent
  title: string
  url: string
  /** Event-specific context, e.g. the booking's start time. */
  detail?: string
  locale: string
}

type T = ReturnType<typeof asLocaleOptions>

export function notificationSubject(event: NotificationTemplateEvent, title: string, t: T): string {
  switch (event) {
    case 'deadline.approaching':
      return m.email_notification_deadline_subject({ title }, t)
    case 'poll.finalized':
      return m.email_notification_finalized_subject({ title }, t)
    case 'booking.created':
      return m.email_notification_booking_created_subject({ title }, t)
    case 'booking.cancelled':
      return m.email_notification_booking_cancelled_subject({ title }, t)
    case 'booking.rescheduled':
      return m.email_notification_booking_rescheduled_subject({ title }, t)
  }
}

function notificationBody(
  event: NotificationTemplateEvent,
  title: string,
  detail: string,
  t: T,
): string {
  switch (event) {
    case 'deadline.approaching':
      return m.email_notification_deadline_body({ title }, t)
    case 'poll.finalized':
      return m.email_notification_finalized_body({ title }, t)
    case 'booking.created':
      return m.email_notification_booking_created_body({ detail }, t)
    case 'booking.cancelled':
      return m.email_notification_booking_cancelled_body({ detail }, t)
    case 'booking.rescheduled':
      return m.email_notification_booking_rescheduled_body({ detail }, t)
  }
}

export function Notification({ event, title, url, detail, locale }: NotificationProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={notificationSubject(event, title, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>{title}</Heading>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {notificationBody(event, title, detail ?? '', t)}
      </Text>
      <Button
        href={url}
        style={{
          backgroundColor: '#e8572a',
          color: '#ffffff',
          padding: '12px 20px',
          borderRadius: '8px',
          fontSize: '14px',
          fontWeight: 'bold',
          textDecoration: 'none',
        }}
      >
        {m.email_notification_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{url}</Text>
    </Layout>
  )
}

export default Notification
