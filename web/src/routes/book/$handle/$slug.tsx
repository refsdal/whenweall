import { useCallback, useEffect } from 'react'
import { createFileRoute, notFound, useRouter } from '@tanstack/react-router'
import * as z from 'zod'
import { appConfig } from '#/app.config'
import { PublicBookingPage } from '#/components/booking/PublicBookingPage'
import { NotFoundCard } from '#/components/layout/NotFoundCard'
import {
  browserTimeZone,
  loadStoredTimeZone,
  storeTimeZone,
} from '#/components/poll/TimezoneSwitch'
import { m } from '#/lib/i18n'
import { useLivePage } from '#/lib/use-live-page'
import { getPublicAvailability } from '#/server/bookings/bookings.functions'

/**
 * `month` is the month the calendar shows and `tz` the zone its days are grouped in — both in
 * the URL so the loader (which runs on the server, where neither is otherwise knowable) can
 * fetch exactly the window being rendered, and so a shared link opens on the same view.
 */
const searchSchema = z.object({
  month: z
    .string()
    .regex(/^\d{4}-(0[1-9]|1[0-2])$/)
    .optional(),
  tz: z.string().max(80).optional(),
})

/**
 * `tz` comes from a URL anyone can edit, and the availability query rejects anything that isn't
 * a real IANA zone — so a hand-typed one is dropped here rather than turned into a 500.
 */
function usableZone(zone: string | undefined): string | undefined {
  if (!zone) return undefined
  try {
    new Intl.DateTimeFormat('en', { timeZone: zone })
    return zone
  } catch {
    return undefined
  }
}

function monthKeyIn(iso: string, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit' })
    .format(new Date(iso))
    .slice(0, 7)
}

/**
 * The `from`/`to` a month needs: the month itself plus a day of padding either side, because
 * slots are grouped into the *visitor's* days — the first and last local day of a month can
 * reach into the neighbouring UTC day in either direction.
 */
function monthWindow(month: string): { from: string; to: string } {
  const [y, mo] = month.split('-').map(Number) as [number, number]
  const first = Date.UTC(y, mo - 1, 1)
  const last = Date.UTC(y, mo, 0)
  return {
    from: new Date(first - 86_400_000).toISOString().slice(0, 10),
    to: new Date(last + 86_400_000).toISOString().slice(0, 10),
  }
}

export const Route = createFileRoute('/book/$handle/$slug')({
  validateSearch: searchSchema,
  loaderDeps: ({ search }) => ({ month: search.month, tz: usableZone(search.tz) }),
  loader: async ({ params, deps }) => {
    const now = new Date().toISOString()
    // No `month` yet: guess in whatever zone we know of (the visitor's, once the client has put
    // it in the URL), then correct below if the page's own zone disagrees — which only happens
    // during the few hours a month where UTC and the organiser's zone straddle a month boundary.
    let month = deps.month ?? monthKeyIn(now, deps.tz ?? 'UTC')

    const fetchMonth = (value: string) =>
      getPublicAvailability({
        data: {
          handle: params.handle,
          slug: params.slug,
          ...monthWindow(value),
        },
      })

    let result = await fetchMonth(month)
    if (!result) throw notFound()

    if (!deps.month) {
      const preferred = monthKeyIn(now, deps.tz ?? result.page.timezone)
      if (preferred !== month) {
        const corrected = await fetchMonth(preferred)
        if (corrected) {
          result = corrected
          month = preferred
        }
      }
    }

    return {
      page: result.page,
      slots: result.slots,
      month,
      timeZone: deps.tz ?? result.page.timezone,
      // The server's clock, sent down so "this month" and the booking horizon render the same on
      // both sides of hydration.
      now,
    }
  },
  head: ({ loaderData }) => {
    const title = loaderData?.page.title ?? m.book_public_not_found_title()
    const owner = loaderData?.page.owner.name
    const description = owner ? m.book_public_organiser({ name: owner }) : appConfig.description
    return {
      meta: [
        { title: `${title} — ${appConfig.name}` },
        { property: 'og:title', content: title },
        { property: 'og:description', content: description },
        { property: 'og:type', content: 'website' },
      ],
    }
  },
  component: BookRoute,
  notFoundComponent: BookNotFound,
})

function BookRoute() {
  const { page, slots, month, timeZone, now } = Route.useLoaderData()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const router = useRouter()

  // The loader can't know the visitor's zone, so the first paint groups slots in the organiser's.
  // Right after mount the browser's own zone (or the one this visitor picked on a poll earlier)
  // goes into the URL, and the loader re-runs with it.
  useEffect(() => {
    if (usableZone(search.tz)) return
    const zone = loadStoredTimeZone() ?? browserTimeZone()
    if (!zone || zone === page.timezone) return
    void navigate({ search: (prev) => ({ ...prev, tz: zone }), replace: true })
  }, [navigate, page.timezone, search.tz])

  // One event covers every change to the page: somebody booked, cancelled or moved a slot.
  // Re-running the loader is the simplest correct response.
  const onEvent = useCallback(() => void router.invalidate(), [router])
  const { connected } = useLivePage(page.id, onEvent)

  // The room broadcasts to sockets that are already open and keeps no history, so anything that
  // changed between the loader running on the server and this socket opening is missed — and,
  // because the next event only reports the *next* change, missed for good. Re-fetching whenever
  // the socket comes up closes that window, and covers reconnects too: a laptop that slept
  // through three bookings comes back to the right list rather than a confidently stale one.
  useEffect(() => {
    if (!connected) return
    void router.invalidate()
  }, [connected, router])

  const onMonthChange = useCallback(
    (next: string) => void navigate({ search: (prev) => ({ ...prev, month: next }) }),
    [navigate],
  )

  const onTimeZoneChange = useCallback(
    (zone: string) => {
      storeTimeZone(zone)
      void navigate({ search: (prev) => ({ ...prev, tz: zone }), replace: true })
    },
    [navigate],
  )

  const onBooked = useCallback(() => router.invalidate(), [router])

  return (
    <PublicBookingPage
      page={page}
      slots={slots}
      now={now}
      month={month}
      timeZone={timeZone}
      onMonthChange={onMonthChange}
      onTimeZoneChange={onTimeZoneChange}
      onBooked={onBooked}
    />
  )
}

function BookNotFound() {
  return (
    <NotFoundCard
      title={m.book_public_not_found_title()}
      body={m.book_public_not_found_body()}
      ctaLabel={m.poll_not_found_cta()}
    />
  )
}
