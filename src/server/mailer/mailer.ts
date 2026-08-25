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

export async function sendMail(
  env: { EMAIL?: SendEmail; EMAIL_FROM: string; APP_ENV?: string },
  msg: MailMessage,
): Promise<boolean> {
  try {
    await createTransport(env).send(msg)
    return true
  } catch (err) {
    // Never log the recipient address here — this is the last line of defence before an error
    // (or its stack trace, in a log aggregator) ends up somewhere less trusted than the request
    // that triggered it.
    console.error(`[mail] failed to send "${msg.subject}"`, err)
    return false
  }
}
