import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

/**
 * The organiser-facing counterpart to `BookingRescheduled.tsx` (which is written from the
 * visitor's point of view: "your booking with {organiser}"). A booking that gets rescheduled —
 * whether the visitor or the organiser themself moved it — must not tell the organiser "New
 * booking" (that's `BookingOrganiserNotice.tsx`, reused for `confirmed`); this template says a
 * booking *moved* and states both the previous and new time.
 */
export type BookingRescheduledOrganiserProps = {
  organiserName: string
  pageTitle: string
  visitorName: string
  previousWhen: string
  when: string
  location?: string | null
  viewUrl: string
  locale: string
}

export function BookingRescheduledOrganiser({
  pageTitle,
  visitorName,
  previousWhen,
  when,
  location,
  viewUrl,
  locale,
}: BookingRescheduledOrganiserProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout
      preview={m.email_booking_rescheduled_org_subject({ title: pageTitle }, t)}
      locale={locale}
    >
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_booking_rescheduled_org_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pageTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_booking_rescheduled_org_body({ name: visitorName, previousWhen, when }, t)}
      </Text>
      {location && (
        <Text style={{ fontSize: '14px', color: '#3f3f46' }}>
          {m.email_booking_location({ location }, t)}
        </Text>
      )}
      <Button
        href={viewUrl}
        style={{
          backgroundColor: '#e8572a',
          color: '#ffffff',
          padding: '12px 20px',
          borderRadius: '8px',
          fontSize: '14px',
          fontWeight: 'bold',
          textDecoration: 'none',
          marginTop: '16px',
        }}
      >
        {m.email_booking_rescheduled_org_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{viewUrl}</Text>
    </Layout>
  )
}

export default BookingRescheduledOrganiser
