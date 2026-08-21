import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import type { MailMessage } from '#/server/mailer/mailer'
import { applyClaim } from '#/server/polls/claims'
import { createPoll, getPollView } from '#/server/polls/service'
import { sendClaimConfirmation } from '#/server/notifications/claim-emails'
import { makeSignupPoll, makeUser } from '../../../../test/helpers'

function recordingMailer(sentMessages: MailMessage[], result = true) {
  return async (_env: unknown, msg: MailMessage): Promise<boolean> => {
    sentMessages.push(msg)
    return result
  }
}

describe('sendClaimConfirmation', () => {
  it('sends a confirmation listing all claimed slots with an ics attachment for date/datetime slots only', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await createPoll(db, ownerId, {
      type: 'signup',
      title: 'Bake sale',
      timezone: 'Europe/Oslo',
      options: [
        { kind: 'datetime', startAt: '2026-09-01T10:00:00.000Z', capacity: null },
        { kind: 'text', label: 'Bring cookies', capacity: null },
      ],
      signupMaxClaims: 2,
    })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [slotA, slotB] = view!.options

    const claim = await applyClaim(db, pollId, slotA!.id, {
      name: 'Alice',
      email: 'alice@example.com',
      userId: null,
      locale: 'en',
    })
    await applyClaim(db, pollId, slotB!.id, { participantId: claim.participantId })

    const sentMessages: MailMessage[] = []
    const ok = await sendClaimConfirmation(
      env,
      { db, pollId, participantId: claim.participantId },
      { mailer: recordingMailer(sentMessages) },
    )

    expect(ok).toBe(true)
    expect(sentMessages).toHaveLength(1)
    const msg = sentMessages[0]!
    expect(msg.to).toBe('alice@example.com')
    expect(msg.subject).toContain('Bake sale')
    expect(msg.html).toContain('Bring cookies')
    expect(msg.attachments).toHaveLength(1)
    expect(msg.attachments![0]!.filename).toBe('calendar.ics')
    expect(msg.attachments![0]!.type).toBe('text/calendar')
    expect(msg.attachments![0]!.content.match(/BEGIN:VEVENT/g)).toHaveLength(1)
  })

  it('skips silently when the participant has no email', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [null] })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const claim = await applyClaim(db, pollId, view!.options[0]!.id, {
      name: 'Alice',
      userId: null,
    })

    const sentMessages: MailMessage[] = []
    const ok = await sendClaimConfirmation(
      env,
      { db, pollId, participantId: claim.participantId },
      { mailer: recordingMailer(sentMessages) },
    )

    expect(ok).toBe(false)
    expect(sentMessages).toHaveLength(0)
  })

  it('returns false without throwing when the participant has no claims', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [null] })

    await expect(
      sendClaimConfirmation(env, { db, pollId, participantId: 'pa_missing' }),
    ).resolves.toBe(false)
  })
})
