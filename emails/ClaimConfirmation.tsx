import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type ClaimConfirmationProps = {
  name: string
  pollTitle: string
  pollUrl: string
  slots: string[]
  locale: string
}

export function ClaimConfirmation({
  name,
  pollTitle,
  pollUrl,
  slots,
  locale,
}: ClaimConfirmationProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={m.email_claim_subject({ title: pollTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_claim_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pollTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_claim_body({ name }, t)}
      </Text>
      {slots.map((slot) => (
        <Text key={slot} style={{ fontSize: '14px', color: '#3f3f46', margin: '0 0 4px' }}>
          {'• '}
          {slot}
        </Text>
      ))}
      <Button
        href={pollUrl}
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
        {m.email_claim_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{pollUrl}</Text>
    </Layout>
  )
}

export default ClaimConfirmation
