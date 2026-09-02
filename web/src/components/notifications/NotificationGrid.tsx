import {
  resolveChannels,
  type NotificationEvent,
  type NotificationGrid as Grid,
} from '#/lib/notifications'
import * as m from '#/paraglide/messages'
import { Checkbox } from '#/components/ui/checkbox'

type Group = { key: string; label: string; events: readonly NotificationEvent[] }

/** Grouped so eleven rows read as four short lists rather than one wall of checkboxes. */
const GROUPS: Group[] = [
  {
    key: 'responses',
    label: m.notif_group_responses(),
    events: ['response.created', 'response.updated', 'response.withdrawn'],
  },
  { key: 'comments', label: m.notif_group_comments(), events: ['comment.created'] },
  {
    key: 'lifecycle',
    label: m.notif_group_lifecycle(),
    events: ['deadline.approaching', 'poll.closed', 'poll.finalized', 'signup.full'],
  },
  {
    key: 'bookings',
    label: m.notif_group_bookings(),
    events: ['booking.created', 'booking.cancelled', 'booking.rescheduled'],
  },
]

function eventLabel(event: NotificationEvent): string {
  switch (event) {
    case 'response.created':
      return m.notif_event_response_created()
    case 'response.updated':
      return m.notif_event_response_updated()
    case 'response.withdrawn':
      return m.notif_event_response_withdrawn()
    case 'comment.created':
      return m.notif_event_comment_created()
    case 'deadline.approaching':
      return m.notif_event_deadline_approaching()
    case 'poll.closed':
      return m.notif_event_poll_closed()
    case 'poll.finalized':
      return m.notif_event_poll_finalized()
    case 'signup.full':
      return m.notif_event_signup_full()
    case 'booking.created':
      return m.notif_event_booking_created()
    case 'booking.cancelled':
      return m.notif_event_booking_cancelled()
    case 'booking.rescheduled':
      return m.notif_event_booking_rescheduled()
  }
}

export type NotificationGridProps = {
  /** Which rows to show — a poll's admin bar passes the poll events, settings passes all of them. */
  events: readonly NotificationEvent[]
  /** The stored grid for this scope. `null` means "inheriting", and every box shows the resolved
   * fallback rather than an empty state. */
  value: Grid | null
  /** The user's account defaults, used as the middle rung of the fallback chain. */
  defaults: Grid | null
  disabled?: boolean
  onChange: (next: Grid) => void
}

export function NotificationGrid({
  events,
  value,
  defaults,
  disabled = false,
  onChange,
}: NotificationGridProps) {
  const visible = GROUPS.map((group) => ({
    ...group,
    events: group.events.filter((event) => events.includes(event)),
  })).filter((group) => group.events.length > 0)

  function toggle(event: NotificationEvent, channel: 'email' | 'push', checked: boolean) {
    // Write the fully resolved row, not a partial one: the stored grid is an override, so it must
    // capture both channels or the untouched one would silently fall back to a different value.
    const current = resolveChannels(event, value, defaults)
    onChange({ ...(value ?? {}), [event]: { ...current, [channel]: checked } })
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-[1fr_auto] items-center gap-x-4 text-xs text-muted-foreground">
        <span />
        <span className="w-12 text-center">{m.notif_channel_email()}</span>
      </div>

      {visible.map((group) => (
        <div key={group.key} className="flex flex-col gap-2">
          <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
            {group.label}
          </p>
          {group.events.map((event) => {
            const channels = resolveChannels(event, value, defaults)
            return (
              <div key={event} className="grid grid-cols-[1fr_auto] items-center gap-x-4 text-sm">
                <span>{eventLabel(event)}</span>
                <span className="flex w-12 justify-center">
                  <Checkbox
                    checked={channels.email}
                    disabled={disabled}
                    aria-label={`${eventLabel(event)} — ${m.notif_channel_email()}`}
                    onCheckedChange={(checked) => toggle(event, 'email', checked === true)}
                  />
                </span>
              </div>
            )
          })}
        </div>
      ))}

      <p className="text-xs text-muted-foreground">{m.notif_email_cadence_hint()}</p>
    </div>
  )
}
