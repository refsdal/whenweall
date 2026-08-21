import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type ClosedProps = { pollTitle: string; pollUrl: string; locale: string }

export function Closed({ pollTitle, pollUrl, locale }: ClosedProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={m.email_closed_subject({ title: pollTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>{pollTitle}</Heading>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_closed_body({}, t)}
      </Text>
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
        }}
      >
        {m.email_closed_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{pollUrl}</Text>
    </Layout>
  )
}

export default Closed
