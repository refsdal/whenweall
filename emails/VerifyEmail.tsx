import { Button, Heading, Text } from '@react-email/components'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type VerifyEmailProps = { name: string; url: string; locale: string }

export function VerifyEmail({ name, url, locale }: VerifyEmailProps) {
  const t = { locale } as { locale: 'en' | 'nb' }
  return (
    <Layout preview={m.email_verify_subject({}, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_verify_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_verify_body({ name }, t)}
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
        {m.email_verify_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{url}</Text>
    </Layout>
  )
}

export default VerifyEmail
