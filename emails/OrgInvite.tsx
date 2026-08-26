import { Button, Heading, Text } from '@react-email/components'
import { asLocaleOptions } from '#/lib/i18n'
import * as m from '#/paraglide/messages'
import { Layout } from './_Layout'

export type OrgInviteProps = { orgName: string; inviterName: string; url: string; locale: string }

export function OrgInvite({ orgName, inviterName, url, locale }: OrgInviteProps) {
  const t = asLocaleOptions(locale)
  return (
    <Layout
      preview={m.email_org_invite_subject({ inviter: inviterName, org: orgName }, t)}
      locale={locale}
    >
      <Heading style={{ fontSize: '20px', margin: '0 0 16px' }}>
        {m.email_org_invite_heading({ org: orgName }, t)}
      </Heading>
      <Text style={{ fontSize: '14px', lineHeight: '22px', color: '#3f3f46' }}>
        {m.email_org_invite_body({ inviter: inviterName, org: orgName }, t)}
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
        {m.email_org_invite_cta({}, t)}
      </Button>
      <Text style={{ fontSize: '12px', color: '#71717a', wordBreak: 'break-all' }}>{url}</Text>
    </Layout>
  )
}

export default OrgInvite
