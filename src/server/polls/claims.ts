import { and, eq, inArray } from 'drizzle-orm'
import type { BatchItem } from 'drizzle-orm/batch'
import type { Db } from '#/server/db/client'
import { participants, pollOptions, polls, votes } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { newId } from '#/lib/ids'
import { generateToken, hashToken } from '#/lib/tokens'
import { LIMITS } from './schemas'

type Query = BatchItem<'sqlite'>

export type ClaimIdentity =
  | { participantId: string }
  | { name: string; email?: string | null; userId: string | null; locale?: string | null }

async function requireOpenSignupPoll(db: Db, pollId: string) {
  const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  if (poll.type !== 'signup') throw new AppError('VALIDATION')
  if (poll.status !== 'open') throw new AppError('POLL_CLOSED')
  return poll
}

export async function countClaims(db: Db, pollId: string): Promise<Record<string, number>> {
  const options = await db.query.pollOptions.findMany({ where: eq(pollOptions.pollId, pollId) })
  const result: Record<string, number> = {}
  for (const option of options) result[option.id] = 0
  if (options.length === 0) return result

  const rows = await db.query.votes.findMany({
    where: and(
      inArray(
        votes.optionId,
        options.map((o) => o.id),
      ),
      eq(votes.answer, 'yes'),
    ),
  })
  for (const vote of rows) {
    result[vote.optionId] = (result[vote.optionId] ?? 0) + 1
  }
  return result
}

export async function applyClaim(
  db: Db,
  pollId: string,
  optionId: string,
  identity: ClaimIdentity,
): Promise<{
  participantId: string
  editToken: string | null
  claimedOptionIds: string[]
  created: boolean
}> {
  const poll = await requireOpenSignupPoll(db, pollId)

  const option = await db.query.pollOptions.findFirst({ where: eq(pollOptions.id, optionId) })
  if (!option || option.pollId !== pollId) throw new AppError('NOT_FOUND')

  let participantId: string
  let editToken: string | null = null
  let created = false
  let name = ''
  let email: string | null = null
  let userId: string | null = null
  let locale: string | null = null

  if ('participantId' in identity) {
    const participant = await db.query.participants.findFirst({
      where: eq(participants.id, identity.participantId),
    })
    if (!participant || participant.pollId !== pollId) throw new AppError('NOT_FOUND')
    participantId = participant.id
  } else {
    const trimmedName = identity.name.trim()
    if (!trimmedName) throw new AppError('VALIDATION')

    const trimmedEmail = identity.email?.trim() ?? ''
    if (poll.requireParticipantEmail && !trimmedEmail) throw new AppError('EMAIL_REQUIRED')

    const existingParticipants = await db.query.participants.findMany({
      where: eq(participants.pollId, pollId),
    })
    if (existingParticipants.length >= LIMITS.participants) throw new AppError('LIMIT_REACHED')

    participantId = `pa_${newId()}`
    created = true
    name = trimmedName
    email = trimmedEmail || null
    userId = identity.userId
    locale = identity.locale ?? null
    editToken = userId === null ? generateToken() : null
  }

  const existingClaims = await db.query.votes.findMany({
    where: eq(votes.participantId, participantId),
  })
  const claimedOptionIds = existingClaims.map((v) => v.optionId)
  const alreadyClaimed = claimedOptionIds.includes(optionId)

  if (!alreadyClaimed && claimedOptionIds.length >= poll.signupMaxClaims) {
    throw new AppError('CLAIM_LIMIT_REACHED')
  }

  if (!alreadyClaimed && option.capacity !== null) {
    const counts = await countClaims(db, pollId)
    if ((counts[optionId] ?? 0) >= option.capacity) throw new AppError('SLOT_FULL')
  }

  if (alreadyClaimed) {
    return { participantId, editToken, claimedOptionIds, created }
  }

  const now = new Date().toISOString()
  const queries: Query[] = []

  if (created) {
    const editTokenHash = editToken ? await hashToken(editToken) : null
    queries.push(
      db.insert(participants).values({
        id: participantId,
        pollId,
        name,
        email,
        userId,
        editTokenHash,
        locale,
        createdAt: now,
        updatedAt: now,
      }),
    )
  }
  queries.push(db.insert(votes).values({ participantId, optionId, answer: 'yes' }))

  await db.batch(queries as [Query, ...Query[]])

  return {
    participantId,
    editToken,
    claimedOptionIds: [...claimedOptionIds, optionId],
    created,
  }
}

export async function removeClaim(
  db: Db,
  pollId: string,
  optionId: string,
  participantId: string,
): Promise<{ remainingOptionIds: string[] }> {
  await requireOpenSignupPoll(db, pollId)

  await db
    .delete(votes)
    .where(and(eq(votes.participantId, participantId), eq(votes.optionId, optionId)))

  const remaining = await db.query.votes.findMany({
    where: eq(votes.participantId, participantId),
  })
  return { remainingOptionIds: remaining.map((v) => v.optionId) }
}
