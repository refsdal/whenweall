import { createServerFn } from '@tanstack/react-start'
import { eq } from 'drizzle-orm'
import * as z from 'zod'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { getLocale } from '#/paraglide/runtime'
import { sessionMiddleware } from '#/server/auth/middleware'
import { rateLimitMiddleware } from '#/server/http/rate-limit.middleware'
import { requireTurnstile } from '#/server/http/turnstile'
import { notifyChanged, queueDigest } from '#/server/notifications/do-client'
import { resolveVerifiedParticipantId } from './comment-auth'
import * as participantService from './participants'
import {
  addCommentSchema,
  addParticipantSchema,
  pollIdSchema,
  updateParticipantSchema,
} from './schemas'

/*
 * A `createServerFn(...)` object doesn't expose its `.middleware([...])` array at runtime (only
 * `method` and `__executeServer` — see test/server-functions.workers.test.ts for how that was
 * confirmed), so these arrays are declared once here and reused both to build each function below
 * and as the manifest that test asserts against. Reusing the same array reference means the
 * manifest can never drift from what a function actually runs.
 */
const SESSION_AND_VOTE_LIMIT = [sessionMiddleware, rateLimitMiddleware('vote')] as const
const SESSION_AND_COMMENT_LIMIT = [sessionMiddleware, rateLimitMiddleware('comment')] as const
const SESSION_ONLY = [sessionMiddleware] as const

export const SERVER_FN_MIDDLEWARE = {
  addParticipant: SESSION_AND_VOTE_LIMIT,
  updateParticipant: SESSION_AND_VOTE_LIMIT,
  removeParticipant: SESSION_AND_VOTE_LIMIT,
  addComment: SESSION_AND_COMMENT_LIMIT,
  deleteComment: SESSION_ONLY,
} as const

/**
 * The comment's display name. For a signed-in author it always comes from the session, never the
 * client-supplied value — otherwise anyone signed in could impersonate another name in their own
 * comments. Guests (no session) keep the name they typed.
 */
export function resolveAuthorName(
  session: { user: { name: string } } | null | undefined,
  clientAuthorName: string,
): string {
  return session?.user.name ?? clientAuthorName
}

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
  .middleware(SERVER_FN_MIDDLEWARE.addParticipant)
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
      locale: getLocale(),
    })

    await queueDigest(data.pollId, { kind: 'vote', name: data.name, at: new Date().toISOString() })
    await notifyChanged(data.pollId, 'participant')

    return result
  })

export const updateParticipant = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.updateParticipant)
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
  .middleware(SERVER_FN_MIDDLEWARE.removeParticipant)
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
  .middleware(SERVER_FN_MIDDLEWARE.addComment)
  .validator(addCommentSchema)
  .handler(async ({ data, context }) => {
    const userId = context.session?.user.id ?? null
    if (!userId) await requireTurnstile(data.turnstileToken)

    const db = getDb()
    const participantId = await resolveVerifiedParticipantId(
      db,
      data.pollId,
      data.participantId,
      data.editToken,
    )
    const authorName = resolveAuthorName(context.session, data.authorName)

    const result = await participantService.addComment(db, data.pollId, {
      authorName,
      body: data.body,
      userId,
      participantId,
    })

    await queueDigest(data.pollId, {
      kind: 'comment',
      name: authorName,
      at: new Date().toISOString(),
    })
    await notifyChanged(data.pollId, 'comment')

    return result
  })

export const deleteComment = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.deleteComment)
  .validator(z.object({ pollId: pollIdSchema, commentId: z.string() }))
  .handler(async ({ data, context }) => {
    const db = getDb()
    const userId = context.session?.user.id ?? null
    const isOwner = await requireIsOwner(db, data.pollId, userId)

    await participantService.deleteComment(db, data.pollId, data.commentId, { userId, isOwner })
    await notifyChanged(data.pollId, 'comment')
  })
