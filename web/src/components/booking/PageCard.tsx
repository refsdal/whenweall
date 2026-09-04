import { Link } from '@tanstack/react-router'
import { CalendarClock, Pencil } from 'lucide-react'
import { bookingPrefix } from '#/components/booking/HandleField'
import { Badge } from '#/components/ui/badge'
import { Button } from '#/components/ui/button'
import { CopyIcon } from '#/components/ui/copy-icon'
import { m } from '#/lib/i18n'
import { useCopy } from '#/lib/use-copy'
import { cn } from '#/lib/utils'
import type { PageSummary } from '#/api/types'

function upcomingLabel(count: number): string {
  if (count === 0) return m.booking_page_upcoming_none()
  return count === 1 ? m.booking_page_upcoming_one() : m.booking_page_upcoming_other({ count })
}

/** One booking page on `/bookings`: what it is, how busy it is, and its public link. */
export function PageCard({
  page,
  handle,
  appUrl,
}: {
  page: PageSummary
  handle: string | null
  appUrl: string
}) {
  const path = `/book/${handle ?? ''}/${page.slug}`
  const display = `${bookingPrefix(appUrl)}${handle ?? ''}/${page.slug}`
  const { copied, copy } = useCopy({
    success: m.booking_page_link_copied(),
    error: m.booking_page_link_copy_failed(),
  })

  return (
    <div data-testid="booking-page-card" className="surface flex h-full flex-col gap-3 p-4">
      <div className="flex items-start justify-between gap-2">
        <Link
          to="/bookings/$id"
          params={{ id: page.id }}
          className="focus-ring flex min-w-0 items-start gap-2.5 rounded-md"
        >
          <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
            <CalendarClock aria-hidden="true" className="size-4" />
          </span>
          <span className="min-w-0 truncate font-medium hover:underline">{page.title}</span>
        </Link>
        <Badge
          className={cn(
            'shrink-0',
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

      <p className="text-sm text-muted-foreground">{upcomingLabel(page.upcomingCount)}</p>

      {handle === null ? (
        <p className="text-sm text-muted-foreground">{m.booking_page_link_needs_handle()}</p>
      ) : (
        <p className="truncate font-mono text-xs text-muted-foreground" title={display}>
          {display}
        </p>
      )}

      <div className="mt-auto flex items-center gap-1.5 pt-1">
        <Button asChild size="sm" variant="outline">
          <Link to="/bookings/$id" params={{ id: page.id }}>
            {m.booking_page_open()}
          </Link>
        </Button>
        <Button asChild size="sm" variant="ghost">
          <Link to="/bookings/$id/edit" params={{ id: page.id }}>
            <Pencil aria-hidden="true" />
            {m.booking_page_edit()}
          </Link>
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="ml-auto"
          disabled={handle === null}
          onClick={() => void copy(`${appUrl}${path}`)}
        >
          <CopyIcon copied={copied} />
          <span className="sr-only sm:not-sr-only">
            {copied ? m.booking_page_link_copied() : m.booking_page_copy_link()}
          </span>
        </Button>
      </div>
    </div>
  )
}
