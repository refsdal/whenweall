import { createFileRoute } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { LegalPage } from '#/components/legal/LegalPage'
import { m } from '#/lib/i18n'

export const Route = createFileRoute('/terms')({
  head: () => ({
    meta: [{ title: `${m.terms_title()} — ${appConfig.name}` }],
  }),
  component: TermsPage,
})

export function TermsPage() {
  return (
    <LegalPage
      title={m.terms_title()}
      updated={m.legal_updated()}
      intro={m.terms_intro()}
      sections={[
        { title: m.terms_service_title(), body: m.terms_service_body() },
        { title: m.terms_accounts_title(), body: m.terms_accounts_body() },
        { title: m.terms_acceptable_use_title(), body: m.terms_acceptable_use_body() },
        { title: m.terms_content_title(), body: m.terms_content_body() },
        { title: m.terms_availability_title(), body: m.terms_availability_body() },
        { title: m.terms_liability_title(), body: m.terms_liability_body() },
        { title: m.terms_law_title(), body: m.terms_law_body() },
        { title: m.terms_changes_title(), body: m.terms_changes_body() },
      ]}
    />
  )
}
