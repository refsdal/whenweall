import { Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type BookingSyncFailedProps = {
  pageTitle: string
  locale: string
}

/**
 * Best-effort notice to the organiser that a Google Calendar create/update/delete failed for one
 * of their bookings (see `sendGoogleSyncFailedNotice` in emails.ts). Deliberately minimal — no
 * booking-specific detail beyond the page title (the failure can happen for a create, a
 * reschedule, or a cancel), and no CTA: the ask is just "go check your calendar".
 */
export function BookingSyncFailed({ pageTitle, locale }: BookingSyncFailedProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={m.email_booking_sync_failed_subject({ title: pageTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_booking_sync_failed_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_booking_sync_failed_body({ title: pageTitle }, t)}
      </Text>
    </Layout>
  )
}

export default BookingSyncFailed
