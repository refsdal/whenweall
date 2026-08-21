import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type BookingCancelledProps = {
  recipientName: string
  pageTitle: string
  when: string
  /** Who cancelled, from this recipient's own point of view: 'you' when the recipient is the one
   * who cancelled, otherwise the *other* party ('organiser' or 'visitor'). */
  cancelledBy: 'you' | 'organiser' | 'visitor'
  visitorName?: string
  viewUrl: string
  locale: string
}

export function BookingCancelled({
  recipientName,
  pageTitle,
  when,
  cancelledBy,
  visitorName,
  viewUrl,
  locale,
}: BookingCancelledProps) {
  const t = asLocaleOptions(locale)
  const body =
    cancelledBy === 'you'
      ? m.email_booking_cancelled_body_you({ name: recipientName, when }, t)
      : cancelledBy === 'organiser'
        ? m.email_booking_cancelled_body_organiser({ name: recipientName, when }, t)
        : m.email_booking_cancelled_body_visitor(
            { name: recipientName, visitor: visitorName ?? '', when },
            t,
          )

  return (
    <Layout preview={m.email_booking_cancelled_subject({ title: pageTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_booking_cancelled_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pageTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>{body}</Text>
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
        {m.email_booking_cancelled_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{viewUrl}</Text>
    </Layout>
  )
}

export default BookingCancelled
