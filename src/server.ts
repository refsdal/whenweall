import handler from '@tanstack/react-start/server-entry'
import { paraglideMiddleware } from './paraglide/server'
import { createDb } from './server/db/client'
import { processMailJob, type MailJob } from './server/mailer/queue'

export { PollRoom } from './do/PollRoom'
export { BookingRoom } from './do/BookingRoom'
export { StatsRoom } from './do/StatsRoom'
export { RateLimitRoom } from './do/RateLimitRoom'

export default {
  fetch(request: Request): Promise<Response> {
    return paraglideMiddleware(request, ({ request: localizedRequest }) =>
      handler.fetch(localizedRequest),
    )
  },

  /**
   * Retries transactional mail that failed to send the first time.
   *
   * Each message is acked or retried on its own rather than the batch as a whole: `batch.retryAll()`
   * would re-deliver messages that already succeeded, and a duplicate booking confirmation is a
   * real cost. A message that exhausts `max_retries` lands in `whenweall-mail-dlq`, which is where
   * "genuinely undeliverable" collects and needs a human — a dead-letter queue nobody reads is the
   * same bug one layer down.
   */
  async queue(batch: MessageBatch<MailJob>, env: Env): Promise<void> {
    const db = createDb(env.DB)

    for (const message of batch.messages) {
      try {
        const outcome = await processMailJob(env, db, message.body)

        if (outcome === 'retry') {
          // `sendBookingEmails` has already logged why via `sendMail`'s structured error.
          message.retry()
        } else {
          message.ack()
        }
      } catch (err) {
        // A thrown error is a bug or a transient dependency failure, not a delivery verdict —
        // either way the message is worth another attempt, and the DLQ bounds how many.
        console.error(
          JSON.stringify({
            event: 'mail.job_threw',
            job: `${message.body?.kind}.${message.body?.event}`,
            attempts: message.attempts,
            error: err instanceof Error ? err.message : String(err),
          }),
        )
        message.retry()
      }
    }
  },
}
