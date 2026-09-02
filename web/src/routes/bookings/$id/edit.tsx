import { createFileRoute, redirect } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { PageEditor } from '#/components/booking/PageEditor'
import { m } from '#/lib/i18n'
import { getBookingPage } from '#/api/bookings'

export const Route = createFileRoute('/bookings/$id/edit')({
  beforeLoad: ({ context, params }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: `/bookings/${params.id}/edit` } })
    }
  },
  loader: ({ params }) => getBookingPage(params.id),
  head: ({ loaderData }) => ({
    meta: [{ title: `${loaderData?.title ?? m.booking_editor_edit_title()} — ${appConfig.name}` }],
  }),
  component: EditBookingPageRoute,
})

function EditBookingPageRoute() {
  const page = Route.useLoaderData()
  const { session, publicConfig } = Route.useRouteContext()

  return (
    <PageEditor
      // Remounts when a different page is opened, so the draft never carries over from the last.
      key={page.id}
      page={page}
      handle={session?.org?.slug ?? null}
      appUrl={window.location.origin}
      googleEnabled={publicConfig.googleEnabled}
    />
  )
}
