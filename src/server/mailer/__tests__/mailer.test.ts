import { afterEach, describe, expect, it, vi } from 'vitest'
import { parseFromAddress, sendMail } from '#/server/mailer/mailer'

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
      EMAIL_FROM: 'whenweall <no-reply@whenweall.com>',
    }

    const ok = await sendMail(env, {
      ...baseMsg,
      attachments: [{ filename: 'invite.ics', content: 'BEGIN:VCALENDAR', type: 'text/calendar' }],
    })

    expect(ok).toBe(true)
    expect(send).toHaveBeenCalledWith({
      from: { name: 'whenweall', email: 'no-reply@whenweall.com' },
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

  it('sends a bare address unchanged when EMAIL_FROM has no display name', async () => {
    const send = vi.fn().mockResolvedValue({ messageId: '1' })
    const env = {
      EMAIL: { send } as unknown as SendEmail,
      EMAIL_FROM: 'no-reply@whenweall.com',
    }

    await sendMail(env, baseMsg)

    expect(send).toHaveBeenCalledWith(expect.objectContaining({ from: 'no-reply@whenweall.com' }))
  })

  it('returns false and logs the subject (never the recipient) when env.EMAIL.send rejects', async () => {
    const send = vi.fn().mockRejectedValue(new Error('boom'))
    const env = {
      EMAIL: { send } as unknown as SendEmail,
      EMAIL_FROM: 'whenweall <no-reply@whenweall.com>',
    }
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})

    const ok = await sendMail(env, baseMsg)

    expect(ok).toBe(false)
    expect(errorSpy).toHaveBeenCalledWith(
      expect.stringContaining(baseMsg.subject),
      expect.any(Error),
    )
    const loggedArgs = errorSpy.mock.calls[0]!
    expect(String(loggedArgs[0])).not.toContain(baseMsg.to)
  })

  it('falls back to console transport and returns true when env.EMAIL is absent', async () => {
    const env = { EMAIL_FROM: 'whenweall <no-reply@whenweall.com>' }
    const logSpy = vi.spyOn(console, 'log').mockImplementation(() => {})

    const ok = await sendMail(env, baseMsg)

    expect(ok).toBe(true)
    expect(logSpy).toHaveBeenCalledWith(expect.stringContaining(`[mail:${baseMsg.to}]`))
  })
})

describe('parseFromAddress', () => {
  it('parses a "Name <addr>" pair', () => {
    expect(parseFromAddress('whenweall <no-reply@whenweall.com>')).toEqual({
      name: 'whenweall',
      email: 'no-reply@whenweall.com',
    })
  })

  it('trims surrounding whitespace and quotes around the name', () => {
    expect(parseFromAddress('  "whenweall"  <no-reply@whenweall.com>  ')).toEqual({
      name: 'whenweall',
      email: 'no-reply@whenweall.com',
    })
  })

  it('returns a bare address unchanged', () => {
    expect(parseFromAddress('no-reply@whenweall.com')).toBe('no-reply@whenweall.com')
  })
})
