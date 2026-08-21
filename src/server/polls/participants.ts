import { and, eq } from 'drizzle-orm'
import type { BatchItem } from 'drizzle-orm/batch'
import type { Db } from '#/server/db/client'
import { comments, participants, pollOptions, polls, votes, type Answer } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { newId } from '#/lib/ids'
import { generateToken, hashToken, verifyToken } from '#/lib/tokens'
import { LIMITS } from './schemas'

type Query = BatchItem<'sqlite'>

export type ParticipantAuth = {
  userId: string | null
  editToken?: string | null
  isOwner: boolean
}

async function requirePollExists(db: Db, pollId: string) {
  const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  return poll
}

async function requireOpenPoll(db: Db, pollId: string) {
  const poll = await requirePollExists(db, pollId)
  if (poll.status !== 'open') throw new AppError('POLL_CLOSED')
  return poll
}

async function validateAnswers(
  db: Db,
  pollId: string,
  answers: Record<string, Answer>,
  allowIfNeedBe: boolean,
): Promise<void> {
  const options = await db.query.pollOptions.findMany({ where: eq(pollOptions.pollId, pollId) })
  const optionIds = new Set(options.map((o) => o.id))
  for (const [optionId, answer] of Object.entries(answers)) {
    if (!optionIds.has(optionId)) throw new AppError('VALIDATION')
    if (answer === 'ifneedbe' && !allowIfNeedBe) throw new AppError('VALIDATION')
  }
}

type VoteRow = { participantId: string; optionId: string; answer: Answer }

function voteRows(participantId: string, answers: Record<string, Answer>): VoteRow[] {
  return Object.entries(answers)
    .filter(([, answer]) => answer !== undefined && answer !== null)
    .map(([optionId, answer]) => ({ participantId, optionId, answer }))
}

export async function addParticipant(
  db: Db,
  pollId: string,
  input: {
    name: string
    email?: string
    answers: Record<string, Answer>
    userId: string | null
    locale?: string | null
  },
): Promise<{ participantId: string; editToken: string | null }> {
  const poll = await requireOpenPoll(db, pollId)

  const trimmedEmail = input.email?.trim() ?? ''
  if (poll.requireParticipantEmail && !trimmedEmail) {
    throw new AppError('EMAIL_REQUIRED')
  }

  const count = await db.query.participants.findMany({ where: eq(participants.pollId, pollId) })
  if (count.length >= LIMITS.participants) throw new AppError('LIMIT_REACHED')

  await validateAnswers(db, pollId, input.answers, poll.allowIfNeedBe)

  const id = `pa_${newId()}`
  const now = new Date().toISOString()
  const isGuest = input.userId === null
  const editToken = isGuest ? generateToken() : null
  const editTokenHash = editToken ? await hashToken(editToken) : null

  const rows = voteRows(id, input.answers)

  const queries: Query[] = [
    db.insert(participants).values({
      id,
      pollId,
      name: input.name,
      email: trimmedEmail || null,
      userId: input.userId,
      editTokenHash,
      locale: input.locale ?? null,
      createdAt: now,
      updatedAt: now,
    }),
  ]
  if (rows.length > 0) {
    queries.push(db.insert(votes).values(rows))
  }

  await db.batch(queries as [Query, ...Query[]])

  return { participantId: id, editToken }
}

async function requireParticipantInPoll(db: Db, pollId: string, participantId: string) {
  const participant = await db.query.participants.findFirst({
    where: eq(participants.id, participantId),
  })
  if (!participant || participant.pollId !== pollId) throw new AppError('NOT_FOUND')
  return participant
}

async function canEditParticipant(
  participant: { userId: string | null; editTokenHash: string | null },
  auth: ParticipantAuth,
): Promise<boolean> {
  if (auth.isOwner) return true
  if (auth.userId && participant.userId === auth.userId) return true
  if (auth.editToken && (await verifyToken(auth.editToken, participant.editTokenHash))) {
    return true
  }
  return false
}

export async function updateParticipant(
  db: Db,
  pollId: string,
  participantId: string,
  auth: ParticipantAuth,
  input: { name?: string; answers: Record<string, Answer> },
): Promise<void> {
  const participant = await requireParticipantInPoll(db, pollId, participantId)
  const poll = await requireOpenPoll(db, pollId)

  if (!(await canEditParticipant(participant, auth))) throw new AppError('FORBIDDEN')

  await validateAnswers(db, pollId, input.answers, poll.allowIfNeedBe)

  const now = new Date().toISOString()
  const update: Partial<typeof participants.$inferInsert> = { updatedAt: now }
  if (input.name !== undefined) update.name = input.name

  const insertRows = voteRows(participantId, input.answers)
  const queries: Query[] = [
    db.update(participants).set(update).where(eq(participants.id, participantId)),
    db.delete(votes).where(eq(votes.participantId, participantId)),
  ]
  if (insertRows.length > 0) {
    queries.push(db.insert(votes).values(insertRows))
  }

  await db.batch(queries as [Query, ...Query[]])
}

export async function removeParticipant(
  db: Db,
  pollId: string,
  participantId: string,
  auth: ParticipantAuth,
): Promise<void> {
  const participant = await requireParticipantInPoll(db, pollId, participantId)
  // Owners may remove a participant regardless of poll status (open, closed, or finalized);
  // everyone else still needs the poll to be open.
  if (auth.isOwner) {
    await requirePollExists(db, pollId)
  } else {
    await requireOpenPoll(db, pollId)
  }

  if (!(await canEditParticipant(participant, auth))) throw new AppError('FORBIDDEN')

  await db.delete(participants).where(eq(participants.id, participantId))
}

export async function addComment(
  db: Db,
  pollId: string,
  input: { authorName: string; body: string; userId: string | null; participantId?: string | null },
): Promise<{ id: string }> {
  const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  if (!poll.allowComments) throw new AppError('FORBIDDEN')

  const id = newId()
  const now = new Date().toISOString()

  await db.insert(comments).values({
    id,
    pollId,
    authorName: input.authorName,
    body: input.body,
    userId: input.userId,
    participantId: input.participantId ?? null,
    createdAt: now,
  })

  return { id }
}

export async function deleteComment(
  db: Db,
  pollId: string,
  commentId: string,
  auth: { userId: string | null; isOwner: boolean },
): Promise<void> {
  const comment = await db.query.comments.findFirst({ where: eq(comments.id, commentId) })
  if (!comment || comment.pollId !== pollId || comment.deletedAt) throw new AppError('NOT_FOUND')

  const allowed = auth.isOwner || (auth.userId !== null && comment.userId === auth.userId)
  if (!allowed) throw new AppError('FORBIDDEN')

  const now = new Date().toISOString()
  await db
    .update(comments)
    .set({ deletedAt: now })
    .where(and(eq(comments.id, commentId), eq(comments.pollId, pollId)))
}
