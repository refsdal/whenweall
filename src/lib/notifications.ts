/**
 * Browser-safe notification catalogue. Lives in `lib/` rather than `server/` because three
 * consumers need it: the server (resolution and delivery), `src/do/protocol.ts` (which must stay
 * free of `#/server/*` imports), and the settings UI. Same reasoning as `src/lib/billing.ts`.
 */
import { z } from 'zod'

export const POLL_NOTIFICATION_EVENTS = [
  'response.created',
  'response.updated',
  'response.withdrawn',
  'comment.created',
  'deadline.approaching',
  'poll.closed',
  'poll.finalized',
  'signup.full',
] as const

export const BOOKING_NOTIFICATION_EVENTS = [
  'booking.created',
  'booking.cancelled',
  'booking.rescheduled',
] as const

export const NOTIFICATION_EVENTS = [
  ...POLL_NOTIFICATION_EVENTS,
  ...BOOKING_NOTIFICATION_EVENTS,
] as const

export type PollNotificationEvent = (typeof POLL_NOTIFICATION_EVENTS)[number]
export type BookingNotificationEvent = (typeof BOOKING_NOTIFICATION_EVENTS)[number]
export type NotificationEvent = (typeof NOTIFICATION_EVENTS)[number]

/**
 * Events that batch through `PollRoom`'s debounced digest. Everything else is singular and
 * time-sensitive — a deadline reminder, a closed poll, a booking made half an hour before its
 * slot — and sends immediately, because waiting out a ten-minute window would make it useless.
 */
export const DIGEST_EVENTS = [
  'response.created',
  'response.updated',
  'response.withdrawn',
  'comment.created',
  'signup.full',
] as const

export type DigestEvent = (typeof DIGEST_EVENTS)[number]

export function isDigestEvent(event: NotificationEvent): event is DigestEvent {
  return (DIGEST_EVENTS as readonly string[]).includes(event)
}

/** Everything that is not batched — exactly the set the single-event email template covers. */
export type ImmediateEvent = Exclude<NotificationEvent, DigestEvent>

export type ChannelPrefs = { email: boolean; push: boolean }
export type NotificationGrid = Partial<Record<NotificationEvent, ChannelPrefs>>

const both: ChannelPrefs = { email: true, push: true }
const emailOnly: ChannelPrefs = { email: true, push: false }
const neither: ChannelPrefs = { email: false, push: false }

/**
 * The grid a user has before they ever open settings. Withdrawals are off because they are the
 * noisiest and least actionable event; push defaults to the handful of things worth interrupting
 * someone for rather than everything email covers.
 */
export const SYSTEM_DEFAULTS: Record<NotificationEvent, ChannelPrefs> = {
  'response.created': both,
  'response.updated': emailOnly,
  'response.withdrawn': neither,
  'comment.created': both,
  'deadline.approaching': both,
  'poll.closed': emailOnly,
  'poll.finalized': emailOnly,
  'signup.full': emailOnly,
  'booking.created': both,
  'booking.cancelled': emailOnly,
  'booking.rescheduled': emailOnly,
}

const channelPrefsSchema = z.object({ email: z.boolean(), push: z.boolean() })

/**
 * Unknown keys are stripped rather than rejected: a stored grid may outlive an event that gets
 * renamed or removed, and a user's whole preference row must not become unreadable because of it.
 */
export const gridSchema: z.ZodType<NotificationGrid> = z
  .record(z.string(), channelPrefsSchema)
  .transform((raw) => {
    const out: NotificationGrid = {}
    for (const event of NOTIFICATION_EVENTS) {
      const value = raw[event]
      if (value) out[event] = value
    }
    return out
  })

/**
 * Precedence: scope override → user default → system default, resolved **per event key**. A
 * whole-object merge would mean overriding one event on one poll silently resets every other
 * event to its system default.
 */
export function resolveChannels(
  event: NotificationEvent,
  override: NotificationGrid | null | undefined,
  defaults: NotificationGrid | null | undefined,
): ChannelPrefs {
  return override?.[event] ?? defaults?.[event] ?? SYSTEM_DEFAULTS[event]
}
