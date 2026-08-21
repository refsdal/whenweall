import { useEffect } from 'react'
import { createFileRoute, notFound, useRouter } from '@tanstack/react-router'
import { MailSearch } from 'lucide-react'
import * as z from 'zod'
import { appConfig } from '#/app.config'
import { ManageBooking } from '#/components/booking/ManageBooking'
import { NotFoundCard } from '#/components/layout/NotFoundCard'
import { useBookingToken } from '#/lib/booking-tokens'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { getManagedBooking } from '#/server/bookings/bookings.functions'

/** `?t=` is the manage token from the confirmation email — the visitor's whole credential. */
const searchSchema = z.object({ t: z.string().optional() })

export const Route = createFileRoute('/booking/$id/')({
  validateSearch: searchSchema,
  loaderDeps: ({ search }) => ({ t: search.t }),
  loader: async ({ params, deps, context }) => {
    const now = new Date().toISOString()
    // No token and nobody signed in: there is nothing to authorise against yet. The component
    // looks in this browser's storage and, if it finds the token, comes back with it in the URL.
    if (!deps.t && !context.session) return { booking: null, now }

    try {
      const booking = await getManagedBooking({ data: { bookingId: params.id, token: deps.t } })
      return { booking, now }
    } catch (error) {
      const code = errorCode(error)
      if (code === 'NOT_FOUND') throw notFound()
      // A wrong or expired token, or a signed-in visitor who isn't the organiser: same friendly
      // "open your link" state rather than an accusing error — the right link fixes it.
      if (code === 'INVALID_TOKEN' || code === 'FORBIDDEN' || code === 'UNAUTHORIZED') {
        return { booking: null, now }
      }
      throw error
    }
  },
  head: ({ loaderData }) => ({
    meta: [
      {
        title: `${loaderData?.booking?.page.title ?? m.booking_manage_title()} — ${appConfig.name}`,
      },
      { name: 'robots', content: 'noindex' },
    ],
  }),
  component: ManageRoute,
  notFoundComponent: BookingNotFound,
})

function ManageRoute() {
  const { booking, now } = Route.useLoaderData()
  const { t } = Route.useSearch()
  const params = Route.useParams()
  const navigate = Route.useNavigate()
  const router = useRouter()

  // The browser that made the booking kept its manage token. Reading it through the external
  // store keeps the server render (always null) and the first client render in agreement; the
  // effect then puts it in the URL so the loader can use it. Only fires when the loader came back
  // empty-handed.
  const storedToken = useBookingToken(params.id)
  useEffect(() => {
    if (booking || t || !storedToken) return
    void navigate({ search: { t: storedToken }, replace: true })
  }, [booking, navigate, storedToken, t])

  if (!booking) {
    return (
      <div className="mx-auto flex w-full max-w-lg flex-col items-center gap-4 px-5 py-24 text-center">
        <span className="inline-flex size-12 items-center justify-center rounded-full bg-secondary text-muted-foreground">
          <MailSearch aria-hidden="true" className="size-5" />
        </span>
        <h1 className="display text-2xl">{m.booking_manage_locked_title()}</h1>
        <p className="text-sm text-balance text-muted-foreground">
          {m.booking_manage_locked_body()}
        </p>
      </div>
    )
  }

  return (
    <ManageBooking booking={booking} now={now} token={t} onChanged={() => router.invalidate()} />
  )
}

function BookingNotFound() {
  return (
    <NotFoundCard
      title={m.booking_manage_not_found_title()}
      body={m.booking_manage_not_found_body()}
      ctaLabel={m.poll_not_found_cta()}
    />
  )
}
