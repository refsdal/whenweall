import { and, eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { member, participants, polls } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { verifyToken } from '#/lib/tokens'
import { canManageContent, type OrgRole } from '#/server/auth/org'

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
export async function requireSignupPoll(
  db: Db,
  pollId: string,
): Promise<{ organizationId: string; createdBy: string | null }> {
  const poll = await db.query.polls.findFirst({
    where: eq(polls.id, pollId),
    columns: { organizationId: true, createdBy: true, deletedAt: true, type: true },
  })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  if (poll.type !== 'signup') throw new AppError('VALIDATION')
  return poll
}

/**
 * Can `userId` manage this poll (org owner/admin, or the poll's own creator)? `null` when there's
 * no signed-in user at all. Used by `claimSlot`/`unclaimSlot`'s `requireParticipantAuth` and by
 * `participants.functions.ts`'s `requireCanManagePoll` — both need "is this the org-side manager
 * acting on someone else's participant/claim", not merely "is this any signed-in user".
 */
export async function canManagePoll(
  db: Db,
  poll: { organizationId: string; createdBy: string | null },
  userId: string | null,
): Promise<boolean> {
  if (userId === null) return false
  const membership = await db.query.member.findFirst({
    where: and(eq(member.organizationId, poll.organizationId), eq(member.userId, userId)),
  })
  if (!membership) return false
  return canManageContent({ role: membership.role as OrgRole }, userId, poll.createdBy)
}

/**
 * Auth for an existing participant acting on a claim: someone who can manage the poll (org
 * owner/admin, or the poll's own creator), the participant's own signed-in user, or someone
 * holding that participant's edit token. Mirrors `participants.ts#canEditParticipant`, but
 * duplicated here (rather than shared) because claims never touch `participantService` — they go
 * straight to the DO, which trusts whatever identity it's handed and does no auth of its own.
 */
export async function requireParticipantAuth(
  db: Db,
  pollId: string,
  participantId: string,
  poll: { organizationId: string; createdBy: string | null },
  auth: { userId: string | null; editToken?: string },
): Promise<void> {
  const participant = await db.query.participants.findFirst({
    where: eq(participants.id, participantId),
  })
  if (!participant || participant.pollId !== pollId) throw new AppError('NOT_FOUND')

  const isSelf = auth.userId !== null && participant.userId === auth.userId
  const hasToken =
    !!auth.editToken && (await verifyToken(auth.editToken, participant.editTokenHash))
  if (isSelf || hasToken) return
  if (await canManagePoll(db, poll, auth.userId)) return
  throw new AppError('FORBIDDEN')
}
