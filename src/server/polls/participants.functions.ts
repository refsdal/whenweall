import { createServerFn } from '@tanstack/react-start'
import { eq } from 'drizzle-orm'
import * as z from 'zod'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import { participants, polls } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { verifyToken } from '#/lib/tokens'
import { sessionMiddleware } from '#/server/auth/middleware'
import { rateLimitMiddleware } from '#/server/http/rate-limit'
import { requireTurnstile } from '#/server/http/turnstile'
import { notifyChanged, queueDigest } from '#/server/notifications/do-client'
import * as participantService from './participants'
import {
  addCommentSchema,
  addParticipantSchema,
  pollIdSchema,
  updateParticipantSchema,
} from './schemas'

/**
 * `isOwner` for participant/comment auth: does the poll belong to the session user? Loaded
 * separately from the participant/comment lookups those services already do, since ownership is
 * an authorization concern the caller (this module) must resolve before delegating.
 */
async function requireIsOwner(db: Db, pollId: string, userId: string | null): Promise<boolean> {
  const poll = await db.query.polls.findFirst({
    where: eq(polls.id, pollId),
    columns: { ownerId: true, deletedAt: true },
  })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  return userId !== null && poll.ownerId === userId
}

export const addParticipant = createServerFn({ method: 'POST' })
  .middleware([sessionMiddleware, rateLimitMiddleware('vote')])
  .validator(addParticipantSchema)
  .handler(async ({ data, context }) => {
    const userId = context.session?.user.id ?? null
    if (!userId) await requireTurnstile(data.turnstileToken)

    const db = getDb()
    const result = await participantService.addParticipant(db, data.pollId, {
      name: data.name,
      email: data.email,
      answers: data.answers,
      userId,
    })

    await queueDigest(data.pollId, { kind: 'vote', name: data.name, at: new Date().toISOString() })
    await notifyChanged(data.pollId, 'participant')

    return result
  })

export const updateParticipant = createServerFn({ method: 'POST' })
  .middleware([sessionMiddleware])
  .validator(updateParticipantSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    const userId = context.session?.user.id ?? null
    const isOwner = await requireIsOwner(db, data.pollId, userId)

    await participantService.updateParticipant(
      db,
      data.pollId,
      data.participantId,
      { userId, editToken: data.editToken ?? null, isOwner },
      { name: data.name, answers: data.answers },
    )
    await notifyChanged(data.pollId, 'vote')
  })

export const removeParticipant = createServerFn({ method: 'POST' })
  .middleware([sessionMiddleware])
  .validator(
    z.object({ pollId: pollIdSchema, participantId: z.string(), editToken: z.string().optional() }),
  )
  .handler(async ({ data, context }) => {
    const db = getDb()
    const userId = context.session?.user.id ?? null
    const isOwner = await requireIsOwner(db, data.pollId, userId)

    await participantService.removeParticipant(db, data.pollId, data.participantId, {
      userId,
      editToken: data.editToken ?? null,
      isOwner,
    })
    await notifyChanged(data.pollId, 'participant')
  })

export const addComment = createServerFn({ method: 'POST' })
  .middleware([sessionMiddleware, rateLimitMiddleware('comment')])
  .validator(addCommentSchema)
  .handler(async ({ data, context }) => {
    const userId = context.session?.user.id ?? null
    if (!userId) await requireTurnstile(data.turnstileToken)

    const db = getDb()

    let participantId: string | null = null
    if (data.participantId && data.editToken) {
      const participant = await db.query.participants.findFirst({
        where: eq(participants.id, data.participantId),
      })
      if (
        participant &&
        participant.pollId === data.pollId &&
        (await verifyToken(data.editToken, participant.editTokenHash))
      ) {
        participantId = participant.id
      }
    }

    const result = await participantService.addComment(db, data.pollId, {
      authorName: data.authorName,
      body: data.body,
      userId,
      participantId,
    })

    await queueDigest(data.pollId, {
      kind: 'comment',
      name: data.authorName,
      at: new Date().toISOString(),
    })
    await notifyChanged(data.pollId, 'comment')

    return result
  })

export const deleteComment = createServerFn({ method: 'POST' })
  .middleware([sessionMiddleware])
  .validator(z.object({ pollId: pollIdSchema, commentId: z.string() }))
  .handler(async ({ data, context }) => {
    const db = getDb()
    const userId = context.session?.user.id ?? null
    const isOwner = await requireIsOwner(db, data.pollId, userId)

    await participantService.deleteComment(db, data.pollId, data.commentId, { userId, isOwner })
    await notifyChanged(data.pollId, 'comment')
  })
