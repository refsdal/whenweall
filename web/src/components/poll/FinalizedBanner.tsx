import { CalendarPlus, PartyPopper } from 'lucide-react'
import { Button } from '#/components/ui/button'
import type { AppLocale } from '#/app.config'
import { m } from '#/lib/i18n'
import { formatOptionLabel } from '#/lib/time'
import type { PollOptionView, PollView } from '#/server/polls/viewmodel'

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

function utcStamp(iso: string): string {
  const date = new Date(iso)
  return (
    `${date.getUTCFullYear()}${pad(date.getUTCMonth() + 1)}${pad(date.getUTCDate())}` +
    `T${pad(date.getUTCHours())}${pad(date.getUTCMinutes())}00Z`
  )
}

function dayStamp(date: string): string {
  return date.replaceAll('-', '')
}

/**
 * A "add to Google Calendar" link for the winning option. Null for text options — "Pizza" has no
 * place in a calendar — and for anything missing a start.
 */
export function googleCalendarUrl(poll: PollView, option: PollOptionView): string | null {
  if (option.kind === 'text' || !option.startAt) return null

  let dates: string
  if (option.kind === 'date') {
    const start = new Date(`${option.startAt}T00:00:00Z`)
    const end = new Date(start.getTime() + 24 * 60 * 60 * 1000)
    dates = `${dayStamp(option.startAt)}/${dayStamp(end.toISOString().slice(0, 10))}`
  } else {
    const endIso =
      option.endAt ?? new Date(new Date(option.startAt).getTime() + 60 * 60 * 1000).toISOString()
    dates = `${utcStamp(option.startAt)}/${utcStamp(endIso)}`
  }

  const params = new URLSearchParams({ action: 'TEMPLATE', text: poll.title, dates })
  if (poll.location) params.set('location', poll.location)
  if (poll.description) params.set('details', poll.description)
  return `https://calendar.google.com/calendar/render?${params.toString()}`
}

/** The payoff: the poll is decided, here is when, here is how to put it in your calendar. */
export function FinalizedBanner({
  poll,
  option,
  locale,
  timeZone,
}: {
  poll: PollView
  option: PollOptionView
  locale: AppLocale
  timeZone: string
}) {
  const label = formatOptionLabel(option, { locale, timeZone })
  const text = [label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')
  const google = googleCalendarUrl(poll, option)

  return (
    <section
      data-testid="finalized-banner"
      className="surface flex flex-col gap-4 border-[var(--yes)]/40 bg-yes-soft/40 p-5 sm:flex-row sm:items-center sm:justify-between"
    >
      <div className="flex items-start gap-3">
        <span className="mt-0.5 inline-flex size-9 shrink-0 items-center justify-center rounded-full bg-yes-soft text-yes-ink">
          <PartyPopper aria-hidden="true" className="size-4.5" />
        </span>
        <div>
          <p className="text-xs font-semibold tracking-wide text-yes-ink uppercase">
            {m.poll_finalized_title()}
          </p>
          <p className="display mt-0.5 text-xl" suppressHydrationWarning>
            {text}
          </p>
          {poll.location && <p className="text-sm text-muted-foreground">{poll.location}</p>}
        </div>
      </div>

      {google && (
        <div className="flex shrink-0 flex-wrap gap-2">
          <Button asChild variant="outline" size="sm">
            <a href={`/p/${poll.id}/calendar.ics`} download>
              <CalendarPlus aria-hidden="true" />
              {m.poll_add_to_calendar()}
            </a>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <a href={google} target="_blank" rel="noreferrer noopener">
              {m.poll_add_to_google()}
            </a>
          </Button>
        </div>
      )}
    </section>
  )
}
