import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type BookingReminderProps = {
  recipientName: string
  pageTitle: string
  when: string
  location?: string | null
  viewUrl: string
  locale: string
}

export function BookingReminder({
  recipientName,
  pageTitle,
  when,
  location,
  viewUrl,
  locale,
}: BookingReminderProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={m.email_booking_reminder_subject({ title: pageTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_booking_reminder_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pageTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_booking_reminder_body({ name: recipientName, when }, t)}
      </Text>
      {location && (
        <Text style={{ fontSize: '14px', color: '#3f3f46' }}>
          {m.email_booking_reminder_location({ location }, t)}
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
        {m.email_booking_reminder_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{viewUrl}</Text>
    </Layout>
  )
}

export default BookingReminder
