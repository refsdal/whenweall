import { createFileRoute } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { PageEditor } from '#/components/booking/PageEditor'
import { m } from '#/lib/i18n'
import { requireVerifiedSession } from '#/lib/session-guard'

export const Route = createFileRoute('/bookings/new')({
  beforeLoad: ({ context }) => requireVerifiedSession(context, '/bookings/new'),
  head: () => ({
    meta: [{ title: `${m.booking_editor_new_title()} — ${appConfig.name}` }],
  }),
  component: NewBookingPageRoute,
})

function NewBookingPageRoute() {
  const { session } = Route.useRouteContext()

  return <PageEditor page={null} handle={session?.org?.slug ?? null} appUrl={window.location.origin} />
}
