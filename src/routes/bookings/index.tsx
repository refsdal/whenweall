import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { CalendarClock, Plus } from 'lucide-react'
import { motion } from 'motion/react'
import { appConfig } from '#/app.config'
import { PageCard } from '#/components/booking/PageCard'
import { bookingPrefix } from '#/components/booking/HandleField'
import { Button, buttonVariants } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { staggerContainer, staggerItem } from '#/lib/motion'
import { cn } from '#/lib/utils'
import { listMyBookingPages } from '#/server/bookings/pages.functions'

export const Route = createFileRoute('/bookings/')({
  beforeLoad: ({ context }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: '/bookings' } })
    }
  },
  loader: () => listMyBookingPages(),
  head: () => ({
    meta: [{ title: `${m.bookings_title()} — ${appConfig.name}` }],
  }),
  component: BookingPagesRoute,
})

/** Nudges an organiser who hasn't picked a handle yet: without one, no page has a working link. */
function HandleNudge({ appUrl }: { appUrl: string }) {
  return (
    <div className="flex flex-col gap-2 rounded-xl border border-dashed border-border bg-accent-soft/40 p-4 sm:flex-row sm:items-center sm:gap-4">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="text-sm font-medium">{m.bookings_handle_nudge_title()}</p>
        <p className="text-sm text-pretty text-muted-foreground">
          {m.bookings_handle_nudge_body({ url: `${bookingPrefix(appUrl)}…` })}
        </p>
      </div>
      <Link
        to="/settings"
        className={cn(buttonVariants({ variant: 'outline', size: 'sm' }), 'shrink-0')}
      >
        {m.bookings_handle_nudge_cta()}
      </Link>
    </div>
  )
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center gap-4 rounded-xl border border-dashed border-border px-6 py-16 text-center">
      <span className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <CalendarClock aria-hidden="true" className="size-6" />
      </span>
      <div className="flex flex-col gap-1">
        <p className="font-medium">{m.bookings_empty_title()}</p>
        <p className="max-w-sm text-sm text-balance text-muted-foreground">
          {m.bookings_empty_body()}
        </p>
      </div>
      <Link to="/bookings/new" className={cn(buttonVariants(), 'gap-1.5')}>
        <Plus aria-hidden="true" />
        {m.bookings_new()}
      </Link>
    </div>
  )
}

function BookingPagesRoute() {
  const pages = Route.useLoaderData()
  const { session, publicConfig } = Route.useRouteContext()
  const handle = session?.org?.slug ?? null

  return (
    <div
      data-testid="booking-pages"
      className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-5 py-10 sm:py-14"
    >
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="display text-3xl">{m.bookings_title()}</h1>
          <p className="text-sm text-muted-foreground">{m.bookings_subtitle()}</p>
        </div>
        <Button asChild size="sm">
          <Link to="/bookings/new">
            <Plus aria-hidden="true" />
            {m.bookings_new()}
          </Link>
        </Button>
      </header>

      {handle === null && <HandleNudge appUrl={publicConfig.appUrl} />}

      {pages.length === 0 ? (
        <EmptyState />
      ) : (
        <motion.ul
          initial="initial"
          animate="animate"
          variants={staggerContainer}
          className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"
        >
          {pages.map((page) => (
            <motion.li key={page.id} variants={staggerItem}>
              <PageCard page={page} handle={handle} appUrl={publicConfig.appUrl} />
            </motion.li>
          ))}
        </motion.ul>
      )}
    </div>
  )
}
