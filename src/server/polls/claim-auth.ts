import { eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { participants, polls } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { verifyToken } from '#/lib/tokens'

/**
 * Extracted from `participants.functions.ts` into its own server-only module so it can be
 * exercised directly in a workers test — same reasoning as `comment-auth.ts`'s
 * `resolveVerifiedParticipantId`: a built `createServerFn(...)` object can't be invoked directly
 * (see `test/server-functions.workers.test.ts`), so the auth logic `claimSlot`/`unclaimSlot` rely
 * on needs to live somewhere testable on its own.
 */

/**
 * Loads a `signup` poll for `claimSlot`/`unclaimSlot`, rejecting a missing poll (`NOT_FOUND`) or a
 * non-signup poll (`VALIDATION`) before either handler calls into the DO.
 */
export async function requireSignupPoll(db: Db, pollId: string): Promise<{ ownerId: string }> {
  const poll = await db.query.polls.findFirst({
    where: eq(polls.id, pollId),
    columns: { ownerId: true, deletedAt: true, type: true },
  })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  if (poll.type !== 'signup') throw new AppError('VALIDATION')
  return poll
}

/**
 * Auth for an existing participant acting on a claim: the poll owner, the participant's own
 * signed-in user, or someone holding that participant's edit token. Mirrors
 * `participants.ts#canEditParticipant`, but duplicated here (rather than shared) because claims
 * never touch `participantService` — they go straight to the DO, which trusts whatever identity
 * it's handed and does no auth of its own.
 */
export async function requireParticipantAuth(
  db: Db,
  pollId: string,
  participantId: string,
  ownerId: string,
  auth: { userId: string | null; editToken?: string },
): Promise<void> {
  const participant = await db.query.participants.findFirst({
    where: eq(participants.id, participantId),
  })
  if (!participant || participant.pollId !== pollId) throw new AppError('NOT_FOUND')

  const isOwner = auth.userId !== null && auth.userId === ownerId
  const isSelf = auth.userId !== null && participant.userId === auth.userId
  const hasToken =
    !!auth.editToken && (await verifyToken(auth.editToken, participant.editTokenHash))
  if (!isOwner && !isSelf && !hasToken) throw new AppError('FORBIDDEN')
}
