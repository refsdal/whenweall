import { createFileRoute } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { LegalPage } from '#/components/legal/LegalPage'
import { m } from '#/lib/i18n'

export const Route = createFileRoute('/privacy')({
  head: () => ({
    meta: [{ title: `${m.privacy_title()} — ${appConfig.name}` }],
  }),
  component: PrivacyPage,
})

export function PrivacyPage() {
  return (
    <LegalPage
      title={m.privacy_title()}
      updated={m.legal_updated()}
      intro={m.privacy_intro()}
      sections={[
        { title: m.privacy_controller_title(), body: m.privacy_controller_body() },
        { title: m.privacy_data_title(), body: m.privacy_data_body() },
        { title: m.privacy_processors_title(), body: m.privacy_processors_body() },
        { title: m.privacy_purposes_title(), body: m.privacy_purposes_body() },
        { title: m.privacy_retention_title(), body: m.privacy_retention_body() },
        { title: m.privacy_visibility_title(), body: m.privacy_visibility_body() },
        { title: m.privacy_cookies_title(), body: m.privacy_cookies_body() },
        { title: m.privacy_rights_title(), body: m.privacy_rights_body() },
      ]}
    />
  )
}
