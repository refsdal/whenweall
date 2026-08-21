import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type BookingConfirmedProps = {
  visitorName: string
  pageTitle: string
  organiserName: string
  when: string
  location?: string | null
  manageUrl: string
  locale: string
}

export function BookingConfirmed({
  visitorName,
  pageTitle,
  organiserName,
  when,
  location,
  manageUrl,
  locale,
}: BookingConfirmedProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={m.email_booking_confirmed_subject({ title: pageTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_booking_confirmed_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pageTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_booking_confirmed_body({ name: visitorName, organiser: organiserName, when }, t)}
      </Text>
      {location && (
        <Text style={{ fontSize: '14px', color: '#3f3f46' }}>
          {m.email_booking_location({ location }, t)}
        </Text>
      )}
      <Button
        href={manageUrl}
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
        {m.email_booking_confirmed_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>
        {manageUrl}
      </Text>
    </Layout>
  )
}

export default BookingConfirmed
