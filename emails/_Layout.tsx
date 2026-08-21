import * as React from 'react'
import { Body, Container, Head, Hr, Html, Preview, Text } from '@react-email/components'
import { appConfig } from '#/app.config'
import * as m from '#/paraglide/messages'

export const ACCENT_COLOR = '#e8572a'

type LayoutProps = {
  preview: string
  locale: string
  children: React.ReactNode
}

export function Layout({ preview, locale, children }: LayoutProps) {
  return (
    <Html>
      <Head />
      <Preview>{preview}</Preview>
      <Body style={{ backgroundColor: '#f4f4f5', fontFamily: 'Helvetica, Arial, sans-serif' }}>
        <Container
          style={{
            backgroundColor: '#ffffff',
            margin: '0 auto',
            padding: '32px',
            maxWidth: '480px',
          }}
        >
          <Text
            style={{
              color: ACCENT_COLOR,
              fontSize: '20px',
              fontWeight: 'bold',
              margin: '0 0 24px',
            }}
          >
            {appConfig.name}
          </Text>
          {children}
          <Hr style={{ borderColor: '#e4e4e7', margin: '32px 0 16px' }} />
          <Text style={{ color: '#71717a', fontSize: '12px', lineHeight: '18px' }}>
            {m.email_footer({ name: appConfig.name }, { locale } as { locale: 'en' | 'nb' })}
            {' · '}
            {appConfig.supportEmail}
          </Text>
        </Container>
      </Body>
    </Html>
  )
}
