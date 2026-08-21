import { afterEach, describe, expect, it, vi } from 'vitest'
import { sendMail } from '#/server/mailer/mailer'

const baseMsg = {
  to: 'ada@example.com',
  subject: 'Hello',
  html: '<p>Hi</p>',
  text: 'Hi',
}

describe('sendMail', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('sends via env.EMAIL and returns true, mapping attachments with disposition attachment', async () => {
    const send = vi.fn().mockResolvedValue({ messageId: '1' })
    const env = {
      EMAIL: { send } as unknown as SendEmail,
      EMAIL_FROM: 'samla <no-reply@samla.app>',
    }

    const ok = await sendMail(env, {
      ...baseMsg,
      attachments: [{ filename: 'invite.ics', content: 'BEGIN:VCALENDAR', type: 'text/calendar' }],
    })

    expect(ok).toBe(true)
    expect(send).toHaveBeenCalledWith({
      from: env.EMAIL_FROM,
      to: baseMsg.to,
      subject: baseMsg.subject,
      html: baseMsg.html,
      text: baseMsg.text,
      attachments: [
        {
          filename: 'invite.ics',
          content: 'BEGIN:VCALENDAR',
          type: 'text/calendar',
          disposition: 'attachment',
        },
      ],
    })
  })

  it('returns false and logs when env.EMAIL.send rejects', async () => {
    const send = vi.fn().mockRejectedValue(new Error('boom'))
    const env = {
      EMAIL: { send } as unknown as SendEmail,
      EMAIL_FROM: 'samla <no-reply@samla.app>',
    }
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    const ok = await sendMail(env, baseMsg)

    expect(ok).toBe(false)
    expect(errorSpy).toHaveBeenCalled()
  })

  it('falls back to console transport and returns true when env.EMAIL is absent', async () => {
    const env = { EMAIL_FROM: 'samla <no-reply@samla.app>' }
    const logSpy = vi.spyOn(console, 'log').mockImplementation(() => {})

    const ok = await sendMail(env, baseMsg)

    expect(ok).toBe(true)
    expect(logSpy).toHaveBeenCalledWith(expect.stringContaining(`[mail:${baseMsg.to}]`))
  })
})
