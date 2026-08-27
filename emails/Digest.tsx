import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import type { DigestEvent } from '#/lib/notifications'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

/** One summarised row in the digest: "3 new responses — Ada, Bob, Cleo". `names` is empty for
 * events where naming the actor adds nothing (a full sign-up sheet). */
export type DigestLine = { event: DigestEvent; names: string[]; count: number }

export type DigestProps = {
  pollTitle: string
  pollUrl: string
  lines: DigestLine[]
  locale: string
}

function lineLabel(line: DigestLine, t: ReturnType<typeof asLocaleOptions>): string {
  const { count } = line
  switch (line.event) {
    case 'response.created':
      return m.email_digest_line_response_created({ count }, t)
    case 'response.updated':
      return m.email_digest_line_response_updated({ count }, t)
    case 'response.withdrawn':
      return m.email_digest_line_response_withdrawn({ count }, t)
    case 'comment.created':
      return m.email_digest_line_comment_created({ count }, t)
    case 'signup.full':
      return m.email_digest_line_signup_full({}, t)
  }
}

export function Digest({ pollTitle, pollUrl, lines, locale }: DigestProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout preview={m.email_digest_subject({ title: pollTitle }, t)} locale={locale}>
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_digest_heading({}, t)}
      </Heading>
      <Text style={{ fontSize: '16px', fontWeight: 'bold', margin: '0 0 8px' }}>{pollTitle}</Text>
      {lines.map((line) => (
        <Text
          key={line.event}
          style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46', margin: '0 0 4px' }}
        >
          {lineLabel(line, t)}
          {line.names.length > 0 ? ` — ${line.names.join(', ')}` : ''}
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
        }}
      >
        {m.email_digest_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{pollUrl}</Text>
    </Layout>
  )
}

export default Digest
