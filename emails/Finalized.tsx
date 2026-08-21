import { Button, Heading, Text } from '@react-email/components'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type FinalizedProps = {
  pollTitle: string
  pollUrl: string
  optionLabel: string
  recipientName: string
  locale: string
}

export function Finalized({
  pollTitle,
  pollUrl,
  optionLabel,
  recipientName,
  locale,
}: FinalizedProps) {
  const t = { locale } as { locale: 'en' | 'nb' }
  return (
    <Layout preview={m.email_finalized_subject({ title: pollTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_finalized_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pollTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_finalized_body({ name: recipientName, option: optionLabel }, t)}
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
        {m.email_finalized_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{pollUrl}</Text>
    </Layout>
  )
}

export default Finalized
