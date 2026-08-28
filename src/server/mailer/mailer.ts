/**
 * Parses `EMAIL_FROM` (e.g. `"whenweall <no-reply@whenweall.com>"`) into the `{ name, email }` shape
 * `SendEmail.send()` expects, so the display name Cloudflare's Email Service shows a recipient
 * actually reflects `wrangler.jsonc`'s configured sender name instead of being ignored. Falls
 * back to the raw string when it isn't a `Name <addr>` pair (e.g. a bare address).
 */
export function parseFromAddress(value: string): string | { name: string; email: string } {
  const match = /^\s*(.*?)\s*<([^<>\s]+)>\s*$/.exec(value)
  if (!match) return value

  const [, rawName, email] = match
  const name = rawName!.replace(/^"(.*)"$/, '$1').trim()
  return name ? { name, email: email! } : email!
}

export type MailAttachment = { filename: string; content: string; type: string }

export type MailMessage = {
  to: string
  subject: string
  html: string
  text: string
  attachments?: MailAttachment[]
}

export interface MailTransport {
  send(msg: MailMessage): Promise<void>
}

export function createTransport(env: {
  EMAIL?: SendEmail
  EMAIL_FROM: string
  APP_ENV?: string
}): MailTransport {
  if (env.EMAIL) {
    const email = env.EMAIL
    const from = parseFromAddress(env.EMAIL_FROM)
    return {
      async send(msg) {
        await email.send({
          from,
          to: msg.to,
          subject: msg.subject,
          html: msg.html,
          text: msg.text,
          attachments: msg.attachments?.map((a) => ({
            filename: a.filename,
            content: a.content,
            type: a.type,
            disposition: 'attachment' as const,
          })),
        })
      },
    }
  }

  return {
    async send(msg) {
      console.log(`[mail:${msg.to}] ${msg.subject}`)
    },
  }
}

/**
 * Cloudflare's send binding reports permanent failures through an error `code`, and the
 * difference matters: `E_RECIPIENT_SUPPRESSED` means the address bounced or marked us as spam and
 * is on an account-level suppression list, so retrying it is pure waste. The transient ones
 * (`E_RATE_LIMIT_EXCEEDED`, `E_DAILY_LIMIT_EXCEEDED`) are worth another attempt later.
 *
 * Returned rather than thrown so the structured log can carry it, and so a future queue consumer
 * can decide between `ack` and `retry` without re-parsing an error message.
 */
function mailErrorCode(err: unknown): string | null {
  const code = (err as { code?: unknown } | null)?.code
  return typeof code === 'string' ? code : null
}

/**
 * Sends one message, reporting success as a boolean rather than throwing.
 *
 * Use `sendMailOrThrow` instead wherever the message *is* the feature — an email that silently
 * fails to arrive is worse than a visible error, because the user is left waiting for something
 * that is never coming.
 */
export async function sendMail(
  env: { EMAIL?: SendEmail; EMAIL_FROM: string; APP_ENV?: string },
  msg: MailMessage,
): Promise<boolean> {
  try {
    await createTransport(env).send(msg)
    return true
  } catch (err) {
    // Structured so it can be counted and alerted on by type, rather than pattern-matched out of
    // prose. Deliberately no recipient address: this is the last line of defence before an error
    // (or its stack trace, in a log aggregator) ends up somewhere less trusted than the request
    // that triggered it.
    console.error(
      JSON.stringify({
        event: 'mail.send_failed',
        subject: msg.subject,
        code: mailErrorCode(err),
        error: err instanceof Error ? err.message : String(err),
      }),
    )
    return false
  }
}

/**
 * `sendMail`, but a failure propagates.
 *
 * For the messages that *are* the feature: email verification, password reset, an org invitation.
 * With `requireEmailVerification: true` a verification email that never arrives leaves an account
 * that cannot be signed into, and the account holder has no way to tell that from a slow inbox.
 * Better to fail the request visibly and let them retry than to report success and strand them.
 */
export async function sendMailOrThrow(
  env: { EMAIL?: SendEmail; EMAIL_FROM: string; APP_ENV?: string },
  msg: MailMessage,
): Promise<void> {
  if (!(await sendMail(env, msg))) {
    throw new Error(`Failed to send "${msg.subject}"`)
  }
}

/**
 * Records that a batch send lost messages.
 *
 * `sendBookingEmails` and `sendFinalizedEmails` already count their failures and return them —
 * the gap was that every caller discarded the number, so a booking confirmation that never
 * reached the visitor looked exactly like one that did. These sends are deliberately best-effort
 * (a booking must not fail because its notification did), so the outcome is logged rather than
 * raised; the structured `event` is what makes it countable and alertable.
 */
export function reportMailOutcome(
  context: string,
  outcome: { sent: number; failed: number },
): void {
  if (outcome.failed === 0) return
  console.error(
    JSON.stringify({
      event: 'mail.batch_incomplete',
      context,
      sent: outcome.sent,
      failed: outcome.failed,
    }),
  )
}
