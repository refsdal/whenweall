import type { Db } from '#/server/db/client'
import type { SendBookingEmailsKind } from '#/server/bookings/emails'

/**
 * A retry request for a batch of transactional mail that failed to send.
 *
 * Carries **ids only** — never a rendered message, a recipient address or a token. Three reasons,
 * in order of importance:
 *
 * 1. Queue storage is one more place personal data would live, subject to the same questions as
 *    every other store (see #39). Ids are not personal data.
 * 2. A retry that re-reads from D1 reflects the world as it is when the retry runs, not as it was
 *    when the send failed. A booking cancelled in between should not have its confirmation
 *    re-sent five minutes later.
 * 3. Messages are small, so a batch stays well inside the queue's per-message limits.
 */
export type MailJob = {
  kind: 'booking'
  event: SendBookingEmailsKind
  bookingId: string
}

/** Anything the producer needs. Narrow so tests can pass a stub queue. */
export type MailQueueEnv = { MAIL_QUEUE?: Queue<MailJob> }

/**
 * Queues a failed send for another attempt.
 *
 * Best-effort by design: this is already the failure path, and a queue that is unreachable must
 * not turn "the confirmation email did not send" into "the booking request failed". The send has
 * already been logged by `reportMailOutcome`, so a lost retry degrades to the previous behaviour
 * rather than losing information.
 */
export async function enqueueMailRetry(env: MailQueueEnv, job: MailJob): Promise<void> {
  if (!env.MAIL_QUEUE) return
  try {
    await env.MAIL_QUEUE.send(job)
  } catch (err) {
    console.error(
      JSON.stringify({
        event: 'mail.enqueue_failed',
        job: `${job.kind}.${job.event}`,
        error: err instanceof Error ? err.message : String(err),
      }),
    )
  }
}

/** What the consumer decided to do with one message, and why. */
export type MailJobOutcome = 'sent' | 'nothing-to-send' | 'retry'

/**
 * Runs one queued retry.
 *
 * Returns `retry` only for a genuine, possibly-transient send failure. A job whose subject has
 * since disappeared — a deleted booking, a page with no recipients left — reports
 * `nothing-to-send` and is acknowledged, because retrying it five times before parking it in the
 * dead-letter queue would be five wasted attempts and one misleading DLQ entry.
 */
export async function processMailJob(
  env: Parameters<typeof import('#/server/bookings/emails').sendBookingEmails>[0],
  db: Db,
  job: MailJob,
): Promise<MailJobOutcome> {
  // Lazily imported for the same reason `BookingRoom` does it: `bookings/emails` pulls React and
  // every booking email component into whatever module graph touches it.
  const { sendBookingEmails } = await import('#/server/bookings/emails')

  // No `manageToken`. The raw token exists only inside the request that minted it and is never
  // recoverable from `manage_token_hash`, so a retry cannot include one — `manageUrl` falls back
  // to the bare booking URL, exactly as the reminder path already does. That is not as lossy as
  // it sounds: the browser that made the booking kept its own copy of the token (see
  // `lib/booking-tokens.ts`), and the manage page reads it from there and puts it back in the URL.
  const { sent, failed } = await sendBookingEmails(env, job.event, job.bookingId, { db })

  if (failed > 0) return 'retry'
  return sent > 0 ? 'sent' : 'nothing-to-send'
}
