import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import type { MailMessage } from '#/server/mailer/mailer'
import { finalizePoll, getPollView } from '#/server/polls/service'
import { sendFinalizedEmails } from '#/server/notifications/finalize-emails'
import { makeParticipant, makePoll, makeUserWithOrg } from '../../../../test/helpers'

async function firstOptionId(
  db: ReturnType<typeof createDb>,
  pollId: string,
  ownerId: string,
): Promise<string> {
  const view = await getPollView(db, pollId, { userId: ownerId })
  return view!.options[0]!.id
}

function recordingMailer(sentMessages: MailMessage[], results: boolean[] = []) {
  let call = 0
  return async (_env: unknown, msg: MailMessage): Promise<boolean> => {
    sentMessages.push(msg)
    const result = results[call] ?? true
    call += 1
    return result
  }
}

describe('sendFinalizedEmails', () => {
  it('sends one email per recipient, each with a calendar.ics attachment', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const optionId = await firstOptionId(db, pollId, ownerId)
    await makeParticipant(db, pollId, 'Alice', {}, { email: 'alice@example.com' })
    await makeParticipant(db, pollId, 'Bob', {}, { email: 'bob@example.com' })

    const result = await finalizePoll(db, pollId, org, ownerId, optionId)
    expect(result.recipients).toHaveLength(3) // Alice + Bob + owner

    const sentMessages: MailMessage[] = []
    const summary = await sendFinalizedEmails(env, result, {
      mailer: recordingMailer(sentMessages),
    })

    expect(summary).toEqual({ sent: 3, failed: 0 })
    expect(sentMessages).toHaveLength(3)
    for (const msg of sentMessages) {
      expect(msg.attachments).toHaveLength(1)
      expect(msg.attachments![0]!.filename).toBe('calendar.ics')
      expect(msg.attachments![0]!.type).toBe('text/calendar')
      expect(msg.attachments![0]!.content).toContain('BEGIN:VCALENDAR')
    }
  })

  it('counts a duplicate participant email once', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const optionId = await firstOptionId(db, pollId, ownerId)
    await makeParticipant(db, pollId, 'Alice', {}, { email: 'dup@example.com' })
    await makeParticipant(db, pollId, 'Alice again', {}, { email: 'dup@example.com' })

    const result = await finalizePoll(db, pollId, org, ownerId, optionId)
    expect(result.recipients).toHaveLength(2) // dup email (once) + owner

    const sentMessages: MailMessage[] = []
    const summary = await sendFinalizedEmails(env, result, {
      mailer: recordingMailer(sentMessages),
    })

    expect(summary.sent).toBe(2)
    expect(sentMessages).toHaveLength(2)
  })

  it('counts a failing send in failed without throwing', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const optionId = await firstOptionId(db, pollId, ownerId)
    await makeParticipant(db, pollId, 'Alice', {}, { email: 'alice@example.com' })

    const result = await finalizePoll(db, pollId, org, ownerId, optionId)
    expect(result.recipients).toHaveLength(2) // Alice + owner

    const sentMessages: MailMessage[] = []
    const summary = await sendFinalizedEmails(env, result, {
      mailer: recordingMailer(sentMessages, [false, true]),
    })

    expect(summary).toEqual({ sent: 1, failed: 1 })
  })
})
