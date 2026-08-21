import { eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { participants } from '#/server/db/schema'
import { verifyToken } from '#/lib/tokens'

/**
 * Resolves a comment's `participantId` only when the caller actually proves ownership of that
 * participant row via a matching edit token — otherwise a guest could tag their comment onto any
 * other participant's votes just by guessing/copying an id. Returns `null` whenever the claimed
 * participant doesn't exist, belongs to a different poll, or the edit token doesn't verify.
 *
 * Extracted from `participants.functions.ts` into its own server-only module so it can be
 * exercised directly in a workers test without hand-constructing a `createServerFn` RPC call.
 */
export async function resolveVerifiedParticipantId(
  db: Db,
  pollId: string,
  participantId: string | null | undefined,
  editToken: string | null | undefined,
): Promise<string | null> {
  if (!participantId || !editToken) return null

  const participant = await db.query.participants.findFirst({
    where: eq(participants.id, participantId),
  })
  if (!participant || participant.pollId !== pollId) return null
  if (!(await verifyToken(editToken, participant.editTokenHash))) return null

  return participant.id
}
