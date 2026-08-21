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
    const from = env.EMAIL_FROM
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
    console.error(`[mail:${msg.to}] failed to send`, err)
    return false
  }
}
