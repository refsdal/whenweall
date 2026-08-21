import { createFileRoute, redirect } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { PageEditor } from '#/components/booking/PageEditor'
import { m } from '#/lib/i18n'

export const Route = createFileRoute('/bookings/new')({
  beforeLoad: ({ context }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: '/bookings/new' } })
    }
  },
  head: () => ({
    meta: [{ title: `${m.booking_editor_new_title()} — ${appConfig.name}` }],
  }),
  component: NewBookingPageRoute,
})

function NewBookingPageRoute() {
  const { session, publicConfig } = Route.useRouteContext()

  return (
    <PageEditor
      page={null}
      handle={session?.user.handle ?? null}
      appUrl={publicConfig.appUrl}
      googleEnabled={publicConfig.googleEnabled}
    />
  )
}
