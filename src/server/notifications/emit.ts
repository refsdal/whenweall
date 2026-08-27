import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import {
  isDigestEvent,
  type BookingNotificationEvent,
  type ImmediateEvent,
  type PollNotificationEvent,
} from '#/lib/notifications'
import { getDb } from '#/server/db/client'
import { bookingPages, bookings, polls } from '#/server/db/schema'
import { sendMail } from '#/server/mailer/mailer'
import { renderNotification } from '#/server/mailer/templates'
import { queueDigest } from '#/server/notifications/do-client'
import { resolveRecipients, type Recipient } from '#/server/notifications/recipients'

export type EmitContext = { actorUserId?: string | null; actorName?: string }
export type BookingEmitContext = EmitContext & { bookingId: string }

/**
 * `mailer` exists for the durable objects. `vi.mock` cannot reach code running inside a DO's own
 * module graph (see the note on `PollRoom.mailer`), so a DO calling `emitPollEvent` has to be able
 * to hand its own overridable mailer down — otherwise every lifecycle email it triggers becomes
 * untestable. Production always leaves this unset and gets the real `sendMail`.
 */
export type EmitDeps = { mailer?: typeof sendMail }

/**
 * The single boundary the rest of the app calls to raise a notification.
 *
 * Best-effort by contract: a stalled durable object or mailer must never fail the request that
 * triggered the notification, so everything here is caught and logged — the same rule
 * `notifyChanged` and `sendClaimConfirmation` already follow.
 *
 * Activity events go through `PollRoom`'s debounced digest; lifecycle events send immediately,
 * because a deadline reminder or a closed poll is singular and time-sensitive and waiting out a
 * ten-minute window would make it useless.
 */
export async function emitPollEvent(
  pollId: string,
  event: PollNotificationEvent,
  ctx: EmitContext = {},
  deps: EmitDeps = {},
): Promise<void> {
  try {
    const db = getDb()
    const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
    if (!poll || poll.deletedAt) return

    const scope = { type: 'poll' as const, id: pollId, organizationId: poll.organizationId }
    const recipients = await resolveRecipients(db, scope, event, {
      actorUserId: ctx.actorUserId ?? null,
    })

    if (isDigestEvent(event)) {
      // Enqueued once for the poll, not once per recipient: preferences are re-resolved at alarm
      // time so a toggle flipped during the debounce window still takes effect. This check only
      // avoids arming an alarm that would deliver to nobody.
      if (recipients.email.length > 0) {
        await queueDigest(pollId, {
          event,
          name: ctx.actorName ?? '',
          at: new Date().toISOString(),
          actorUserId: ctx.actorUserId ?? null,
        })
      }
    } else {
      await sendImmediate(
        recipients.email,
        { event, title: poll.title, url: `${env.APP_URL}/p/${pollId}` },
        deps,
      )
    }

    // Push delivery lands in Phase 2. `recipients.push` is already resolved and entitlement-gated
    // here so that phase adds a delivery call rather than a second resolution path.
  } catch (err) {
    console.error(`[notifications] emitPollEvent(${event}) failed`, err)
  }
}

export async function emitBookingEvent(
  pageId: string,
  event: BookingNotificationEvent,
  ctx: BookingEmitContext,
  deps: EmitDeps = {},
): Promise<void> {
  try {
    const db = getDb()
    const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
    if (!page || page.deletedAt) return

    const scope = {
      type: 'booking_page' as const,
      id: pageId,
      organizationId: page.organizationId,
    }
    const recipients = await resolveRecipients(db, scope, event, {
      actorUserId: ctx.actorUserId ?? null,
    })
    if (recipients.email.length === 0) return

    const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, ctx.bookingId) })

    await sendImmediate(
      recipients.email,
      {
        event,
        title: page.title,
        url: `${env.APP_URL}/bookings/${pageId}`,
        detail: booking?.startAt ?? '',
      },
      deps,
    )
  } catch (err) {
    console.error(`[notifications] emitBookingEvent(${event}) failed`, err)
  }
}

/** `allSettled` so one bad address cannot stop the rest — same rule as `sendFinalizedEmails`. */
async function sendImmediate(
  recipients: Recipient[],
  msg: { event: ImmediateEvent; title: string; url: string; detail?: string },
  deps: EmitDeps = {},
): Promise<void> {
  if (recipients.length === 0) return
  const mailer = deps.mailer ?? sendMail

  await Promise.allSettled(
    recipients.map(async (recipient) => {
      const rendered = await renderNotification({ ...msg, locale: recipient.locale })
      await mailer(env, { to: recipient.email, ...rendered })
    }),
  )
}
