import { createFileRoute, Link, redirect, useRouter } from '@tanstack/react-router'
import { useServerFn } from '@tanstack/react-start'
import { ArrowLeft, MapPin, Pencil } from 'lucide-react'
import { toast } from 'sonner'
import { appConfig } from '#/app.config'
import { BookingsTable } from '#/components/booking/BookingsTable'
import { bookingPrefix } from '#/components/booking/HandleField'
import { Badge } from '#/components/ui/badge'
import { Button } from '#/components/ui/button'
import { CopyIcon } from '#/components/ui/copy-icon'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { m } from '#/lib/i18n'
import { useCopy } from '#/lib/use-copy'
import { cn } from '#/lib/utils'
import { cancelBooking, listPageBookings } from '#/server/bookings/bookings.functions'
import { getBookingPage } from '#/server/bookings/pages.functions'

/** A year either side: enough to hold every booking an organiser still cares to look at. */
const WINDOW_MS = 365 * 86_400_000

export const Route = createFileRoute('/bookings/$id/')({
  beforeLoad: ({ context, params }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: `/bookings/${params.id}` } })
    }
  },
  loader: async ({ params }) => {
    const now = new Date()
    const [page, bookings] = await Promise.all([
      getBookingPage({ data: { pageId: params.id } }),
      listPageBookings({
        data: {
          pageId: params.id,
          from: new Date(now.getTime() - WINDOW_MS).toISOString(),
          to: new Date(now.getTime() + WINDOW_MS).toISOString(),
        },
      }),
    ])
    // Sent down with the data so the upcoming/past split renders identically on both sides.
    return { page, bookings, now: now.toISOString() }
  },
  head: ({ loaderData }) => ({
    meta: [{ title: `${loaderData?.page.title ?? m.bookings_title()} — ${appConfig.name}` }],
  }),
  component: BookingPageRoute,
})

function PublicLink({ url, disabled }: { url: string; disabled: boolean }) {
  const { copied, copy } = useCopy({
    success: m.booking_page_link_copied(),
    error: m.booking_page_link_copy_failed(),
  })

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="booking-public-link">{m.booking_page_link_label()}</Label>
      <div className="flex gap-2">
        <Input
          id="booking-public-link"
          readOnly
          value={url}
          onFocus={(event) => event.currentTarget.select()}
          className="font-mono text-xs sm:text-sm"
        />
        <Button
          type="button"
          variant="outline"
          disabled={disabled}
          className="shrink-0"
          onClick={() => void copy(url)}
        >
          <CopyIcon copied={copied} />
          <span className="max-sm:sr-only">{m.booking_page_copy_link()}</span>
        </Button>
      </div>
      {disabled && (
        <p className="text-sm text-muted-foreground">{m.booking_page_link_needs_handle()}</p>
      )}
    </div>
  )
}

function BookingPageRoute() {
  const { page, bookings, now } = Route.useLoaderData()
  const { session, publicConfig } = Route.useRouteContext()
  const router = useRouter()
  const cancelFn = useServerFn(cancelBooking)

  const handle = session?.org?.slug ?? null
  const publicUrl = `${publicConfig.appUrl}/book/${handle ?? ''}/${page.slug}`
  const display = `${bookingPrefix(publicConfig.appUrl)}${handle ?? ''}/${page.slug}`

  async function handleCancel(bookingId: string) {
    try {
      await cancelFn({ data: { bookingId } })
      toast.success(m.bookings_cancelled_toast())
      await router.invalidate()
    } catch {
      toast.error(m.bookings_cancel_error())
    }
  }

  return (
    <div
      data-testid="booking-page-view"
      className="mx-auto flex w-full max-w-4xl flex-col gap-6 px-5 py-10 sm:py-14"
    >
      <Link
        to="/bookings"
        className="focus-ring -mb-2 inline-flex w-fit items-center gap-1.5 rounded-md text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft aria-hidden="true" className="size-3.5" />
        {m.booking_view_back()}
      </Link>

      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="display text-3xl break-words">{page.title}</h1>
            <Badge
              className={cn(
                page.status === 'active'
                  ? 'bg-yes-soft text-yes-ink'
                  : 'bg-ifneedbe-soft text-ifneedbe-ink',
              )}
            >
              {page.status === 'active'
                ? m.booking_page_status_active()
                : m.booking_page_status_paused()}
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            {m.booking_editor_duration_minutes({ count: page.slotDurationMin })}
            {' · '}
            {page.timezone.replace(/_/g, ' ')}
          </p>
          {page.location && (
            <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
              <MapPin aria-hidden="true" className="size-3.5" />
              {page.location}
            </p>
          )}
        </div>
        <Button asChild size="sm" variant="outline">
          <Link to="/bookings/$id/edit" params={{ id: page.id }}>
            <Pencil aria-hidden="true" />
            {m.booking_view_edit()}
          </Link>
        </Button>
      </header>

      {page.description && (
        <p className="text-sm text-pretty text-muted-foreground">{page.description}</p>
      )}

      <section className="surface flex flex-col gap-3 p-4 sm:p-6">
        <PublicLink url={handle === null ? display : publicUrl} disabled={handle === null} />
      </section>

      <section className="surface flex flex-col gap-4 p-4 sm:p-6">
        <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
          {m.booking_view_bookings_title()}
        </h2>
        <BookingsTable
          bookings={bookings}
          timezone={page.timezone}
          now={now}
          onCancel={handleCancel}
        />
      </section>
    </div>
  )
}
