import { createFileRoute, redirect } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { CreatorWizard } from '#/components/creator/CreatorWizard'
import { m } from '#/lib/i18n'

export const Route = createFileRoute('/new')({
  beforeLoad: ({ context }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: '/new' } })
    }
  },
  head: () => ({
    meta: [{ title: `${m.creator_page_title()} — ${appConfig.name}` }],
  }),
  component: NewPollPage,
})

function NewPollPage() {
  return <CreatorWizard />
}
