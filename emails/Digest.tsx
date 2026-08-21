import { Button, Heading, Text } from '@react-email/components'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type DigestProps = {
  pollTitle: string
  pollUrl: string
  newVoters: string[]
  newComments: number
  locale: string
}

export function Digest({ pollTitle, pollUrl, newVoters, newComments, locale }: DigestProps) {
  const t = { locale } as { locale: 'en' | 'nb' }
  return (
    <Layout preview={m.email_digest_subject({ title: pollTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_digest_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pollTitle}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_digest_voters({ count: newVoters.length }, t)}
      </Text>
      <Text style={{ fontSize: '14px', color: '#3f3f46' }}>{newVoters.join(', ')}</Text>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_digest_comments({ count: newComments }, t)}
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
        {m.email_digest_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{pollUrl}</Text>
    </Layout>
  )
}

export default Digest
