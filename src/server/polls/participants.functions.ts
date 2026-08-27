import { createServerFn } from '@tanstack/react-start'
import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import * as z from 'zod'
import type { Db } from '#/server/db/client'
import { getDb } from '#/server/db/client'
import { participants, polls } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { getLocale } from '#/paraglide/runtime'
import { sessionMiddleware } from '#/server/auth/middleware'
import { rateLimitMiddleware } from '#/server/http/rate-limit.middleware'
import { requireTurnstile } from '#/server/http/turnstile'
import { claimViaRoom, notifyChanged, unclaimViaRoom } from '#/server/notifications/do-client'
import { emitPollEvent } from '#/server/notifications/emit'
import { recordResponses } from '#/server/stats/stats-client'
import { sendClaimConfirmation } from '#/server/notifications/claim-emails'
import { canManagePoll, requireParticipantAuth, requireSignupPoll } from './claim-auth'
import { resolveVerifiedParticipantId } from './comment-auth'
import type { ClaimIdentity } from './claims'
import * as participantService from './participants'
import {
  addCommentSchema,
  addParticipantSchema,
  claimSchema,
  pollIdSchema,
  unclaimSchema,
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
  claimSlot: SESSION_AND_VOTE_LIMIT,
  unclaimSlot: SESSION_AND_VOTE_LIMIT,
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
 * `isOwner` for participant/comment auth: can this session user manage the poll (org owner/admin,
 * or the poll's own creator)? Loaded separately from the participant/comment lookups those
 * services already do, since authorization is a concern the caller (this module) must resolve
 * before delegating.
 */
async function requireIsOwner(db: Db, pollId: string, userId: string | null): Promise<boolean> {
  const poll = await db.query.polls.findFirst({
    where: eq(polls.id, pollId),
    columns: { organizationId: true, createdBy: true, deletedAt: true },
  })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  return canManagePoll(db, poll, userId)
}

/**
 * `addParticipant`/`updateParticipant` write plain (non-claim) votes — for a `signup` poll those
 * writes must only ever happen via `claimSlot`/`unclaimSlot` (capacity is only enforced there, via
 * the DO), so both handlers below reject a signup poll before touching `participantService` at all.
 */
async function requireNotSignupPoll(db: Db, pollId: string): Promise<void> {
  const poll = await db.query.polls.findFirst({
    where: eq(polls.id, pollId),
    columns: { type: true, deletedAt: true },
  })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  if (poll.type === 'signup') throw new AppError('VALIDATION')
}

export const addParticipant = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.addParticipant)
  .validator(addParticipantSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    await requireNotSignupPoll(db, data.pollId)

    const userId = context.session?.user.id ?? null
    if (!userId) await requireTurnstile(data.turnstileToken)

    const result = await participantService.addParticipant(db, data.pollId, {
      name: data.name,
      email: data.email,
      answers: data.answers,
      userId,
      locale: getLocale(),
    })

    await emitPollEvent(data.pollId, 'response.created', {
      actorName: data.name,
      actorUserId: userId,
    })
    await recordResponses(Object.values(data.answers))
    await notifyChanged(data.pollId, 'participant')

    return result
  })

export const updateParticipant = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.updateParticipant)
  .validator(updateParticipantSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    await requireNotSignupPoll(db, data.pollId)

    const userId = context.session?.user.id ?? null
    const isOwner = await requireIsOwner(db, data.pollId, userId)

    // Read the current name before the update: `data.name` is optional on an answers-only edit,
    // and after the write the previous name is gone.
    const existing = await db.query.participants.findFirst({
      where: eq(participants.id, data.participantId),
      columns: { name: true },
    })

    await participantService.updateParticipant(
      db,
      data.pollId,
      data.participantId,
      { userId, editToken: data.editToken ?? null, isOwner },
      { name: data.name, answers: data.answers },
    )
    await emitPollEvent(data.pollId, 'response.updated', {
      actorName: data.name ?? existing?.name ?? '',
      actorUserId: userId,
    })
    // An edit is a fresh submission — see the spec's §1 on why the totals do not net out.
    await recordResponses(Object.values(data.answers))
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

    const existing = await db.query.participants.findFirst({
      where: eq(participants.id, data.participantId),
      columns: { name: true },
    })

    await participantService.removeParticipant(db, data.pollId, data.participantId, {
      userId,
      editToken: data.editToken ?? null,
      isOwner,
    })
    await emitPollEvent(data.pollId, 'response.withdrawn', {
      actorName: existing?.name ?? '',
      actorUserId: userId,
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

    await emitPollEvent(data.pollId, 'comment.created', {
      actorName: authorName,
      actorUserId: userId,
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

export const claimSlot = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.claimSlot)
  .validator(claimSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    const poll = await requireSignupPoll(db, data.pollId)
    const userId = context.session?.user.id ?? null

    let identity: ClaimIdentity
    if (data.participantId) {
      await requireParticipantAuth(db, data.pollId, data.participantId, poll, {
        userId,
        editToken: data.editToken,
      })
      identity = { participantId: data.participantId }
    } else {
      if (!data.name) throw new AppError('VALIDATION')
      if (!userId) await requireTurnstile(data.turnstileToken)
      identity = { name: data.name, email: data.email, userId, locale: getLocale() }
    }

    // `PollRoom#claim` runs the write inside the DO (serialised per poll) and broadcasts
    // `poll.changed`/'vote' itself — no separate `notifyChanged` call is needed here.
    const result = await claimViaRoom(data.pollId, data.optionId, identity)

    // A re-claim of a slot already held is a no-op (`changed: false`) — nothing changed, so don't
    // queue a digest entry or re-send the confirmation email for it.
    if (result.changed) {
      const claimant = await db.query.participants.findFirst({
        where: eq(participants.id, result.participantId),
        columns: { name: true },
      })
      await emitPollEvent(data.pollId, 'response.created', {
        actorName: claimant?.name ?? data.name ?? '',
        actorUserId: userId,
      })
      // A sign-up claim is stored as a `yes` vote (see `applyClaim`), so it counts as one.
      await recordResponses(['yes'])

      // Best-effort: `sendClaimConfirmation` never throws (it catches and logs internally), so a
      // stalled mailer must never fail a claim that already succeeded.
      await sendClaimConfirmation(env, {
        db,
        pollId: data.pollId,
        participantId: result.participantId,
      })
    }

    return {
      participantId: result.participantId,
      editToken: result.editToken,
      claimedOptionIds: result.claimedOptionIds,
    }
  })

export const unclaimSlot = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.unclaimSlot)
  .validator(unclaimSchema)
  .handler(async ({ data, context }) => {
    const db = getDb()
    const poll = await requireSignupPoll(db, data.pollId)
    const userId = context.session?.user.id ?? null
    const isOwner = await canManagePoll(db, poll, userId)

    await requireParticipantAuth(db, data.pollId, data.participantId, poll, {
      userId,
      editToken: data.editToken,
    })

    // `PollRoom#unclaim` broadcasts `poll.changed`/'vote' itself; see the note in `claimSlot`. The
    // owner may free up a spot on a closed sheet; anyone acting on their own claim still needs it
    // open — `requireSignupPoll`/`removeClaim` enforce that via `allowClosed`.
    const claimant = await db.query.participants.findFirst({
      where: eq(participants.id, data.participantId),
      columns: { name: true },
    })

    const result = await unclaimViaRoom(data.pollId, data.optionId, data.participantId, {
      allowClosed: isOwner,
    })

    await emitPollEvent(data.pollId, 'response.withdrawn', {
      actorName: claimant?.name ?? '',
      actorUserId: userId,
    })

    // Best-effort, same as in `claimSlot`: resend the confirmation so the participant's email
    // reflects their remaining claims. `sendClaimConfirmation` itself sends nothing once none are
    // left, and never throws.
    await sendClaimConfirmation(env, {
      db,
      pollId: data.pollId,
      participantId: data.participantId,
    })

    return { remainingOptionIds: result.remainingOptionIds }
  })
