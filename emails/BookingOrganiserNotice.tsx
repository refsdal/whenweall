import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type BookingOrganiserNoticeProps = {
  organiserName: string
  pageTitle: string
  visitorName: string
  visitorEmail: string
  visitorNote?: string | null
  when: string
  location?: string | null
  viewUrl: string
  locale: string
}

export function BookingOrganiserNotice({
  pageTitle,
  visitorName,
  visitorEmail,
  visitorNote,
  when,
  location,
  viewUrl,
  locale,
}: BookingOrganiserNoticeProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={m.email_booking_organiser_subject({ title: pageTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_booking_organiser_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pageTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_booking_organiser_body({ name: visitorName, email: visitorEmail, when }, t)}
      </Text>
      {location && (
        <Text style={{ fontSize: '14px', color: '#3f3f46' }}>
          {m.email_booking_location({ location }, t)}
        </Text>
      )}
      {visitorNote && (
        <Text style={{ fontSize: '14px', color: '#3f3f46' }}>
          {m.email_booking_organiser_note({ note: visitorNote }, t)}
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
        {m.email_booking_organiser_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{viewUrl}</Text>
    </Layout>
  )
}

export default BookingOrganiserNotice
