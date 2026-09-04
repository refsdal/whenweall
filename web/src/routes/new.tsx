import { createFileRoute } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { CreatorWizard } from '#/components/creator/CreatorWizard'
import { m } from '#/lib/i18n'
import { requireVerifiedSession } from '#/lib/session-guard'

export const Route = createFileRoute('/new')({
  beforeLoad: ({ context }) => requireVerifiedSession(context, '/new'),
  head: () => ({
    meta: [{ title: `${m.creator_page_title()} — ${appConfig.name}` }],
  }),
  component: NewPollPage,
})

function NewPollPage() {
  return <CreatorWizard />
}
