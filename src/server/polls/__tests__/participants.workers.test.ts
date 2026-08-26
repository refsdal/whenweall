import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { participants } from '#/server/db/schema'
import { makePoll, makeUser, makeUserWithOrg } from '../../../../test/helpers'
import { createPoll, deletePoll, finalizePoll, getPollView, setPollStatus } from '#/server/polls/service'
import type { PollOptionView } from '#/server/polls/viewmodel'
import {
  addComment,
  addParticipant,
  deleteComment,
  removeParticipant,
  updateParticipant,
} from '#/server/polls/participants'

async function pollOptions(
  db: ReturnType<typeof createDb>,
  pollId: string,
  ownerId: string,
): Promise<PollOptionView[]> {
  const view = await getPollView(db, pollId, { userId: ownerId })
  return view!.options
}

describe('addParticipant', () => {
  it('creates a participant with votes and returns a 43-char guest editToken', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1, opt2] = await pollOptions(db, pollId, ownerId)

    const result = await addParticipant(db, pollId, {
      name: 'Alice',
      answers: { [opt1!.id]: 'yes', [opt2!.id]: 'no' },
      userId: null,
    })

    expect(result.participantId).toBeTruthy()
    expect(result.editToken).toHaveLength(43)

    const view = await getPollView(db, pollId, { userId: ownerId })
    const alice = view?.participants.find((p) => p.id === result.participantId)
    expect(alice?.votes).toEqual({ [opt1!.id]: 'yes', [opt2!.id]: 'no' })
  })

  it('only stores votes for options the participant answered', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1, opt2] = await pollOptions(db, pollId, ownerId)

    const result = await addParticipant(db, pollId, {
      name: 'Alice',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })

    const view = await getPollView(db, pollId, { userId: ownerId })
    const alice = view?.participants.find((p) => p.id === result.participantId)
    expect(alice?.votes).toEqual({ [opt1!.id]: 'yes' })
    expect(Object.keys(alice!.votes)).not.toContain(opt2!.id)
  })

  it('stores votes for all 100 options on a maximally-sized poll without exceeding D1 bound-parameter limits', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'options',
        title: 'Huge options poll',
        timezone: 'Europe/Oslo',
        options: Array.from({ length: 100 }, (_, i) => ({
          kind: 'text' as const,
          label: `Option ${i}`,
        })),
      },
    )
    const options = await pollOptions(db, pollId, ownerId)
    expect(options).toHaveLength(100)
    const answers = Object.fromEntries(options.map((o) => [o.id, 'yes' as const]))

    const result = await addParticipant(db, pollId, { name: 'Alice', answers, userId: null })

    const view = await getPollView(db, pollId, { userId: ownerId })
    const alice = view?.participants.find((p) => p.id === result.participantId)
    expect(Object.keys(alice!.votes)).toHaveLength(100)
  })

  it('stores the given locale', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    const result = await addParticipant(db, pollId, {
      name: 'Alice',
      answers: {},
      userId: null,
      locale: 'nb',
    })

    const row = await db.query.participants.findFirst({
      where: eq(participants.id, result.participantId),
    })
    expect(row?.locale).toBe('nb')
  })

  it('returns a null editToken for a signed-in participant (session-based editing)', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: voterId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1] = await pollOptions(db, pollId, ownerId)

    const result = await addParticipant(db, pollId, {
      name: 'Voter',
      answers: { [opt1!.id]: 'yes' },
      userId: voterId,
    })

    expect(result.editToken).toBeNull()

    const row = await db.query.participants.findFirst({
      where: eq(participants.id, result.participantId),
    })
    expect(row?.editTokenHash).toBeNull()
    expect(row?.userId).toBe(voterId)
  })

  it('throws NOT_FOUND for a missing poll', async () => {
    const db = createDb(env.DB)
    await expect(
      addParticipant(db, 'missing12345', { name: 'Alice', answers: {}, userId: null }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws NOT_FOUND for a soft-deleted poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    await deletePoll(db, pollId, org, ownerId)

    await expect(
      addParticipant(db, pollId, { name: 'Alice', answers: {}, userId: null }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws POLL_CLOSED when the poll is not open', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    await expect(
      addParticipant(db, pollId, { name: 'Alice', answers: {}, userId: null }),
    ).rejects.toMatchObject({ code: 'POLL_CLOSED' })
  })

  it('throws EMAIL_REQUIRED when the poll requires an email and none (or blank) is given', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(
      db,
      { orgId, createdBy: ownerId },
      { requireParticipantEmail: true },
    )

    await expect(
      addParticipant(db, pollId, { name: 'Alice', answers: {}, userId: null }),
    ).rejects.toMatchObject({ code: 'EMAIL_REQUIRED' })

    await expect(
      addParticipant(db, pollId, { name: 'Alice', email: '   ', answers: {}, userId: null }),
    ).rejects.toMatchObject({ code: 'EMAIL_REQUIRED' })
  })

  it('accepts a trimmed email when the poll requires one', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(
      db,
      { orgId, createdBy: ownerId },
      { requireParticipantEmail: true },
    )

    const result = await addParticipant(db, pollId, {
      name: 'Alice',
      email: '  alice@example.com  ',
      answers: {},
      userId: null,
    })
    expect(result.participantId).toBeTruthy()
  })

  it('throws VALIDATION when an answer references an option not on the poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await expect(
      addParticipant(db, pollId, { name: 'Alice', answers: { bogus: 'yes' }, userId: null }),
    ).rejects.toMatchObject({ code: 'VALIDATION' })
  })

  it('throws VALIDATION for an ifneedbe answer when the poll disallows it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(
      db,
      { orgId, createdBy: ownerId },
      { allowIfNeedBe: false },
    )
    const [opt1] = await pollOptions(db, pollId, ownerId)

    await expect(
      addParticipant(db, pollId, {
        name: 'Alice',
        answers: { [opt1!.id]: 'ifneedbe' },
        userId: null,
      }),
    ).rejects.toMatchObject({ code: 'VALIDATION' })
  })

  it('throws LIMIT_REACHED once the poll has 500 participants', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const now = new Date().toISOString()
    const rows = Array.from({ length: 500 }, (_, i) => ({
      id: `pa_limit_${i}`,
      pollId,
      name: `P${i}`,
      createdAt: now,
      updatedAt: now,
    }))
    const chunkSize = 20
    for (let i = 0; i < rows.length; i += chunkSize) {
      await db.insert(participants).values(rows.slice(i, i + chunkSize))
    }

    await expect(
      addParticipant(db, pollId, { name: 'Overflow', answers: {}, userId: null }),
    ).rejects.toMatchObject({ code: 'LIMIT_REACHED' })
  }, 30_000)
})

describe('updateParticipant', () => {
  async function setup(db: ReturnType<typeof createDb>) {
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1, opt2] = await pollOptions(db, pollId, ownerId)
    return { ownerId, org, pollId, opt1: opt1!, opt2: opt2! }
  }

  it('lets the owner update the name and replace votes', async () => {
    const db = createDb(env.DB)
    const { ownerId, pollId, opt1, opt2 } = await setup(db)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Alice',
      answers: { [opt1.id]: 'yes' },
      userId: null,
    })

    await updateParticipant(
      db,
      pollId,
      participantId,
      { userId: null, isOwner: true },
      { name: 'Alice Renamed', answers: { [opt2.id]: 'no' } },
    )

    const view = await getPollView(db, pollId, { userId: ownerId })
    const alice = view?.participants.find((p) => p.id === participantId)
    expect(alice?.name).toBe('Alice Renamed')
    expect(alice?.votes).toEqual({ [opt2.id]: 'no' })
  })

  it('lets the participant update via a matching editToken', async () => {
    const db = createDb(env.DB)
    const { pollId, opt1 } = await setup(db)
    const { participantId, editToken } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1.id]: 'yes' },
      userId: null,
    })

    await updateParticipant(
      db,
      pollId,
      participantId,
      { userId: null, editToken, isOwner: false },
      { answers: { [opt1.id]: 'no' } },
    )

    const view = await getPollView(db, pollId, { userId: null })
    expect(view?.participants.find((p) => p.id === participantId)?.votes).toEqual({
      [opt1.id]: 'no',
    })
  })

  it('throws FORBIDDEN for a wrong editToken', async () => {
    const db = createDb(env.DB)
    const { pollId, opt1 } = await setup(db)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1.id]: 'yes' },
      userId: null,
    })

    await expect(
      updateParticipant(
        db,
        pollId,
        participantId,
        { userId: null, editToken: 'wrong-token', isOwner: false },
        { answers: { [opt1.id]: 'no' } },
      ),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws FORBIDDEN when neither owner, matching userId, nor a valid editToken is given', async () => {
    const db = createDb(env.DB)
    const { pollId, opt1 } = await setup(db)
    const { id: otherUserId } = await makeUser(db)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1.id]: 'yes' },
      userId: null,
    })

    await expect(
      updateParticipant(
        db,
        pollId,
        participantId,
        { userId: otherUserId, isOwner: false },
        { answers: { [opt1.id]: 'no' } },
      ),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('lets a signed-in participant update via a matching userId', async () => {
    const db = createDb(env.DB)
    const { pollId, opt1 } = await setup(db)
    const { id: voterId } = await makeUser(db)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Voter',
      answers: { [opt1.id]: 'yes' },
      userId: voterId,
    })

    await updateParticipant(
      db,
      pollId,
      participantId,
      { userId: voterId, isOwner: false },
      { answers: { [opt1.id]: 'no' } },
    )

    const view = await getPollView(db, pollId, { userId: null })
    expect(view?.participants.find((p) => p.id === participantId)?.votes).toEqual({
      [opt1.id]: 'no',
    })
  })

  it('throws NOT_FOUND when the participant does not belong to the poll', async () => {
    const db = createDb(env.DB)
    const { ownerId, org, pollId } = await setup(db)
    const { id: otherPollId } = await makePoll(db, { orgId: org.id, createdBy: ownerId })
    const [otherOpt] = await pollOptions(db, otherPollId, ownerId)
    const { participantId } = await addParticipant(db, otherPollId, {
      name: 'Elsewhere',
      answers: { [otherOpt!.id]: 'yes' },
      userId: null,
    })

    await expect(
      updateParticipant(
        db,
        pollId,
        participantId,
        { userId: null, isOwner: true },
        { answers: {} },
      ),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws POLL_CLOSED once the poll is no longer open, even for the owner', async () => {
    const db = createDb(env.DB)
    const { ownerId, org, pollId, opt1 } = await setup(db)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1.id]: 'yes' },
      userId: null,
    })
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    await expect(
      updateParticipant(
        db,
        pollId,
        participantId,
        { userId: null, isOwner: true },
        { answers: { [opt1.id]: 'no' } },
      ),
    ).rejects.toMatchObject({ code: 'POLL_CLOSED' })
  })

  it('throws VALIDATION when an answer references an option not on the poll', async () => {
    const db = createDb(env.DB)
    const { pollId, opt1 } = await setup(db)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1.id]: 'yes' },
      userId: null,
    })

    await expect(
      updateParticipant(
        db,
        pollId,
        participantId,
        { userId: null, isOwner: true },
        { answers: { bogus: 'yes' } },
      ),
    ).rejects.toMatchObject({ code: 'VALIDATION' })
  })

  it('throws VALIDATION for an ifneedbe answer when the poll disallows it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(
      db,
      { orgId, createdBy: ownerId },
      { allowIfNeedBe: false },
    )
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })

    await expect(
      updateParticipant(
        db,
        pollId,
        participantId,
        { userId: null, isOwner: true },
        { answers: { [opt1!.id]: 'ifneedbe' } },
      ),
    ).rejects.toMatchObject({ code: 'VALIDATION' })
  })
})

describe('removeParticipant', () => {
  it('lets the owner remove any participant', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })

    await removeParticipant(db, pollId, participantId, { userId: null, isOwner: true })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.participants.some((p) => p.id === participantId)).toBe(false)
  })

  it('lets the participant remove themselves via editToken', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId, editToken } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })

    await removeParticipant(db, pollId, participantId, {
      userId: null,
      editToken,
      isOwner: false,
    })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.participants.some((p) => p.id === participantId)).toBe(false)
  })

  it('throws FORBIDDEN for an unrelated user', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: otherUserId } = await makeUser(db)
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })

    await expect(
      removeParticipant(db, pollId, participantId, { userId: otherUserId, isOwner: false }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws NOT_FOUND when the participant does not belong to the poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: otherPollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [otherOpt] = await pollOptions(db, otherPollId, ownerId)
    const { participantId } = await addParticipant(db, otherPollId, {
      name: 'Elsewhere',
      answers: { [otherOpt!.id]: 'yes' },
      userId: null,
    })

    await expect(
      removeParticipant(db, pollId, participantId, { userId: null, isOwner: true }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('lets the owner remove a participant once the poll is closed', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    await removeParticipant(db, pollId, participantId, { userId: null, isOwner: true })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.participants.some((p) => p.id === participantId)).toBe(false)
  })

  it('lets the owner remove a participant once the poll is finalized', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })
    await finalizePoll(db, pollId, org, ownerId, opt1!.id)

    await removeParticipant(db, pollId, participantId, { userId: null, isOwner: true })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.participants.some((p) => p.id === participantId)).toBe(false)
  })

  it('throws POLL_CLOSED for a non-owner once the poll is no longer open', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId, editToken } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    await expect(
      removeParticipant(db, pollId, participantId, { userId: null, editToken, isOwner: false }),
    ).rejects.toMatchObject({ code: 'POLL_CLOSED' })
  })

  it('throws NOT_FOUND for the owner when the poll is soft-deleted', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const [opt1] = await pollOptions(db, pollId, ownerId)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Bob',
      answers: { [opt1!.id]: 'yes' },
      userId: null,
    })
    await deletePoll(db, pollId, org, ownerId)

    await expect(
      removeParticipant(db, pollId, participantId, { userId: null, isOwner: true }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})

describe('addComment', () => {
  it('creates a comment and returns its id', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    const { id } = await addComment(db, pollId, {
      authorName: 'Alice',
      body: 'Looks good',
      userId: null,
    })

    expect(id).toBeTruthy()
    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.comments.map((c) => c.id)).toContain(id)
  })

  it('throws NOT_FOUND for a missing poll', async () => {
    const db = createDb(env.DB)
    await expect(
      addComment(db, 'missing12345', { authorName: 'Alice', body: 'x', userId: null }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws FORBIDDEN when the poll disallows comments', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(
      db,
      { orgId, createdBy: ownerId },
      { allowComments: false },
    )

    await expect(
      addComment(db, pollId, { authorName: 'Alice', body: 'x', userId: null }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('allows comments on a closed or finalized poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    const { id } = await addComment(db, pollId, { authorName: 'Alice', body: 'x', userId: null })
    expect(id).toBeTruthy()

    const view0 = await getPollView(db, pollId, { userId: ownerId })
    await finalizePoll(db, pollId, org, ownerId, view0!.options[0]!.id)
    const { id: id2 } = await addComment(db, pollId, { authorName: 'Bob', body: 'y', userId: null })
    expect(id2).toBeTruthy()
  })
})

describe('deleteComment', () => {
  it('lets the owner delete any comment, excluding it from getPollView', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: commentId } = await addComment(db, pollId, {
      authorName: 'Alice',
      body: 'x',
      userId: null,
    })

    await deleteComment(db, pollId, commentId, { userId: ownerId, isOwner: true })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.comments.some((c) => c.id === commentId)).toBe(false)
  })

  it('lets the author delete their own comment via userId', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: authorId } = await makeUser(db)
    const { id: commentId } = await addComment(db, pollId, {
      authorName: 'Alice',
      body: 'x',
      userId: authorId,
    })

    await deleteComment(db, pollId, commentId, { userId: authorId, isOwner: false })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.comments.some((c) => c.id === commentId)).toBe(false)
  })

  it('throws FORBIDDEN for an unrelated user', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: authorId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: commentId } = await addComment(db, pollId, {
      authorName: 'Alice',
      body: 'x',
      userId: authorId,
    })

    await expect(
      deleteComment(db, pollId, commentId, { userId: otherId, isOwner: false }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws NOT_FOUND when the comment does not belong to the poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: otherPollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: commentId } = await addComment(db, otherPollId, {
      authorName: 'Alice',
      body: 'x',
      userId: null,
    })

    await expect(
      deleteComment(db, pollId, commentId, { userId: null, isOwner: true }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws NOT_FOUND when deleting an already-deleted comment', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: commentId } = await addComment(db, pollId, {
      authorName: 'Alice',
      body: 'x',
      userId: null,
    })
    await deleteComment(db, pollId, commentId, { userId: ownerId, isOwner: true })

    await expect(
      deleteComment(db, pollId, commentId, { userId: ownerId, isOwner: true }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})
