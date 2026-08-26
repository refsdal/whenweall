import { createServerFn } from '@tanstack/react-start'
import { notFound } from '@tanstack/react-router'
import { env } from 'cloudflare:workers'
import * as z from 'zod'
import { getDb } from '#/server/db/client'
import { sessionMiddleware } from '#/server/auth/middleware'
import { requireOrgMiddleware } from '#/server/auth/org'
import { rateLimitMiddleware } from '#/server/http/rate-limit.middleware'
import { notifyChanged, syncDeadline } from '#/server/notifications/do-client'
import { emitPollEvent } from '#/server/notifications/emit'
import { sendFinalizedEmails } from '#/server/notifications/finalize-emails'
import * as pollService from './service'
import {
  createPollSchema,
  notificationPrefsSchema,
  pollFollowingSchema,
  pollIdSchema,
  updatePollSchema,
} from './schemas'

/*
 * A `createServerFn(...)` object doesn't expose its `.middleware([...])` array at runtime (only
 * `method` and `__executeServer` — see test/server-functions.workers.test.ts for how that was
 * confirmed), so these arrays are declared once here and reused both to build each function below
 * and as the manifest that test asserts against. Reusing the same array reference means the
 * manifest can never drift from what a function actually runs.
 */
const SESSION_ONLY = [sessionMiddleware] as const
const REQUIRE_ORG = [requireOrgMiddleware] as const
const REQUIRE_ORG_AND_CREATE_LIMIT = [requireOrgMiddleware, rateLimitMiddleware('create')] as const

export const SERVER_FN_MIDDLEWARE = {
  getPoll: SESSION_ONLY,
  createPoll: REQUIRE_ORG_AND_CREATE_LIMIT,
  updatePoll: REQUIRE_ORG,
  setPollStatus: REQUIRE_ORG,
  finalizePoll: REQUIRE_ORG,
  deletePoll: REQUIRE_ORG,
  duplicatePoll: REQUIRE_ORG,
  listMyPolls: REQUIRE_ORG,
  updateNotificationPrefs: REQUIRE_ORG,
  setPollFollowing: REQUIRE_ORG,
} as const

export const getPoll = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.getPoll)
  .validator(z.object({ pollId: pollIdSchema }))
  .handler(async ({ data, context }) => {
    const view = await pollService.getPollView(getDb(), data.pollId, {
      userId: context.session?.user.id ?? null,
    })
    if (!view) throw notFound()
    return view
  })

export const createPoll = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.createPoll)
  .validator(createPollSchema)
  .handler(async ({ data, context }) => {
    const result = await pollService.createPoll(
      getDb(),
      { organizationId: context.org.id, createdBy: context.session.user.id },
      data,
    )
    await syncDeadline(result.id, data.deadlineAt ?? null)
    return result
  })

export const updatePoll = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.updatePoll)
  .validator(updatePollSchema)
  .handler(async ({ data, context }) => {
    const { pollId, ...input } = data
    await pollService.updatePoll(getDb(), pollId, context.org, context.session.user.id, input)
    await notifyChanged(pollId, 'poll')
    // `deadlineAt` is optional on this schema: `undefined` means "leave it unchanged" (service.ts
    // only touches the column when the field is present), so the DO's alarm must only be
    // re-synced when the caller actually provided a value — otherwise an update that doesn't
    // touch the deadline would wrongly clear a still-active deadline alarm.
    if (input.deadlineAt !== undefined) {
      await syncDeadline(pollId, input.deadlineAt ?? null)
    }
  })

export const setPollStatus = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.setPollStatus)
  .validator(z.object({ pollId: pollIdSchema, status: z.enum(['open', 'closed']) }))
  .handler(async ({ data, context }) => {
    await pollService.setPollStatus(
      getDb(),
      data.pollId,
      context.org,
      context.session.user.id,
      data.status,
    )
    await notifyChanged(data.pollId, 'poll')
  })

export const finalizePoll = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.finalizePoll)
  .validator(z.object({ pollId: pollIdSchema, optionId: z.string() }))
  .handler(async ({ data, context }) => {
    const result = await pollService.finalizePoll(
      getDb(),
      data.pollId,
      context.org,
      context.session.user.id,
      data.optionId,
    )
    await notifyChanged(data.pollId, 'poll')
    // Participants always get the transactional "the time is set" mail with its .ics — that is
    // not a notification and is never gated by preferences. The organiser-side notice below is.
    const { sent } = await sendFinalizedEmails(env, result)
    await emitPollEvent(data.pollId, 'poll.finalized', {
      actorUserId: context.session.user.id,
    })
    await syncDeadline(data.pollId, null)
    return { sent }
  })

export const deletePoll = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.deletePoll)
  .validator(z.object({ pollId: pollIdSchema }))
  .handler(async ({ data, context }) => {
    await pollService.deletePoll(getDb(), data.pollId, context.org, context.session.user.id)
    await notifyChanged(data.pollId, 'poll')
    await syncDeadline(data.pollId, null)
  })

export const duplicatePoll = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.duplicatePoll)
  .validator(z.object({ pollId: pollIdSchema }))
  .handler(async ({ data, context }) => {
    return pollService.duplicatePoll(getDb(), data.pollId, context.org, context.session.user.id)
  })

export const listMyPolls = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.listMyPolls)
  .handler(async ({ context }) => {
    return pollService.listMyPolls(getDb(), context.org.id)
  })

export const updateNotificationPrefs = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.updateNotificationPrefs)
  .validator(notificationPrefsSchema)
  .handler(async ({ data, context }) => {
    await pollService.updateNotificationPrefs(
      getDb(),
      data.pollId,
      context.org,
      context.session.user.id,
      data.channels,
    )
  })

export const setPollFollowing = createServerFn({ method: 'POST' })
  .middleware(SERVER_FN_MIDDLEWARE.setPollFollowing)
  .validator(pollFollowingSchema)
  .handler(async ({ data, context }) => {
    await pollService.setPollFollowing(
      getDb(),
      data.pollId,
      context.org,
      context.session.user.id,
      data.following,
    )
  })
