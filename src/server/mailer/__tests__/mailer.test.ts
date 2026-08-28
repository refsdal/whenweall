import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  parseFromAddress,
  reportMailOutcome,
  sendMail,
  sendMailOrThrow,
} from '#/server/mailer/mailer'

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
    // One structured argument now, not a message plus the Error — see `sendMail`.
    expect(errorSpy).toHaveBeenCalledTimes(1)
    const logged = JSON.parse(errorSpy.mock.calls[0]![0] as string)
    expect(logged.subject).toBe(baseMsg.subject)
    expect(logged.error).toBe('boom')
    expect(String(errorSpy.mock.calls[0]![0])).not.toContain(baseMsg.to)
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

describe('sendMail failure reporting', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  function failingEnv(err: unknown) {
    return {
      EMAIL: { send: vi.fn().mockRejectedValue(err) } as unknown as SendEmail,
      EMAIL_FROM: 'whenweall <no-reply@whenweall.com>',
    }
  }

  it('logs a structured event so failures can be counted by type, not grepped from prose', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const err = Object.assign(new Error('recipient suppressed'), {
      code: 'E_RECIPIENT_SUPPRESSED',
    })

    expect(await sendMail(failingEnv(err), baseMsg)).toBe(false)

    expect(error).toHaveBeenCalledTimes(1)
    expect(JSON.parse(error.mock.calls[0]![0] as string)).toEqual({
      event: 'mail.send_failed',
      subject: 'Hello',
      code: 'E_RECIPIENT_SUPPRESSED',
      error: 'recipient suppressed',
    })
  })

  // The recipient address is the one thing that must never reach a log aggregator from here.
  it('never logs the recipient address', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})

    await sendMail(failingEnv(new Error('boom')), baseMsg)

    expect(error.mock.calls[0]![0]).not.toContain('ada@example.com')
  })

  it('reports a null code when the failure carries none', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})

    await sendMail(failingEnv(new Error('boom')), baseMsg)

    expect(JSON.parse(error.mock.calls[0]![0] as string).code).toBeNull()
  })
})

describe('sendMailOrThrow', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('resolves when the message goes out', async () => {
    const env = {
      EMAIL: { send: vi.fn().mockResolvedValue({ messageId: '1' }) } as unknown as SendEmail,
      EMAIL_FROM: 'whenweall <no-reply@whenweall.com>',
    }

    await expect(sendMailOrThrow(env, baseMsg)).resolves.toBeUndefined()
  })

  // The whole point: with `requireEmailVerification: true`, a verification mail that silently
  // fails leaves an account nobody can sign in to, and the user cannot tell that from a slow inbox.
  it('throws when the message does not, so the caller can surface it', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const env = {
      EMAIL: { send: vi.fn().mockRejectedValue(new Error('boom')) } as unknown as SendEmail,
      EMAIL_FROM: 'whenweall <no-reply@whenweall.com>',
    }

    await expect(sendMailOrThrow(env, baseMsg)).rejects.toThrow('Failed to send "Hello"')
  })
})

describe('reportMailOutcome', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('stays quiet when every message went out', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})

    reportMailOutcome('booking.confirmed', { sent: 3, failed: 0 })

    expect(error).not.toHaveBeenCalled()
  })

  it('records a partial batch, which the callers used to discard silently', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})

    reportMailOutcome('booking.confirmed', { sent: 1, failed: 2 })

    expect(JSON.parse(error.mock.calls[0]![0] as string)).toEqual({
      event: 'mail.batch_incomplete',
      context: 'booking.confirmed',
      sent: 1,
      failed: 2,
    })
  })
})
