import { createServerFn } from '@tanstack/react-start'
import { notFound } from '@tanstack/react-router'
import { env } from 'cloudflare:workers'
import * as z from 'zod'
import { getDb } from '#/server/db/client'
import { requireSessionMiddleware, sessionMiddleware } from '#/server/auth/middleware'
import { rateLimitMiddleware } from '#/server/http/rate-limit.middleware'
import { notifyChanged, syncDeadline } from '#/server/notifications/do-client'
import { sendFinalizedEmails } from '#/server/notifications/finalize-emails'
import * as pollService from './service'
import {
  createPollSchema,
  notificationPrefsSchema,
  pollIdSchema,
  updatePollSchema,
} from './schemas'

export const getPoll = createServerFn({ method: 'GET' })
  .middleware([sessionMiddleware])
  .validator(z.object({ pollId: pollIdSchema }))
  .handler(async ({ data, context }) => {
    const view = await pollService.getPollView(getDb(), data.pollId, {
      userId: context.session?.user.id ?? null,
    })
    if (!view) throw notFound()
    return view
  })

export const createPoll = createServerFn({ method: 'POST' })
  .middleware([requireSessionMiddleware, rateLimitMiddleware('create')])
  .validator(createPollSchema)
  .handler(async ({ data, context }) => {
    const result = await pollService.createPoll(getDb(), context.session.user.id, data)
    await syncDeadline(result.id, data.deadlineAt ?? null)
    return result
  })

export const updatePoll = createServerFn({ method: 'POST' })
  .middleware([requireSessionMiddleware])
  .validator(updatePollSchema)
  .handler(async ({ data, context }) => {
    const { pollId, ...input } = data
    await pollService.updatePoll(getDb(), pollId, context.session.user.id, input)
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
  .middleware([requireSessionMiddleware])
  .validator(z.object({ pollId: pollIdSchema, status: z.enum(['open', 'closed']) }))
  .handler(async ({ data, context }) => {
    await pollService.setPollStatus(getDb(), data.pollId, context.session.user.id, data.status)
    await notifyChanged(data.pollId, 'poll')
  })

export const finalizePoll = createServerFn({ method: 'POST' })
  .middleware([requireSessionMiddleware])
  .validator(z.object({ pollId: pollIdSchema, optionId: z.string() }))
  .handler(async ({ data, context }) => {
    const result = await pollService.finalizePoll(
      getDb(),
      data.pollId,
      context.session.user.id,
      data.optionId,
    )
    await notifyChanged(data.pollId, 'poll')
    const { sent } = await sendFinalizedEmails(env, result)
    await syncDeadline(data.pollId, null)
    return { sent }
  })

export const deletePoll = createServerFn({ method: 'POST' })
  .middleware([requireSessionMiddleware])
  .validator(z.object({ pollId: pollIdSchema }))
  .handler(async ({ data, context }) => {
    await pollService.deletePoll(getDb(), data.pollId, context.session.user.id)
  })

export const duplicatePoll = createServerFn({ method: 'POST' })
  .middleware([requireSessionMiddleware])
  .validator(z.object({ pollId: pollIdSchema }))
  .handler(async ({ data, context }) => {
    return pollService.duplicatePoll(getDb(), data.pollId, context.session.user.id)
  })

export const listMyPolls = createServerFn({ method: 'GET' })
  .middleware([requireSessionMiddleware])
  .handler(async ({ context }) => {
    return pollService.listMyPolls(getDb(), context.session.user.id)
  })

export const updateNotificationPrefs = createServerFn({ method: 'POST' })
  .middleware([requireSessionMiddleware])
  .validator(notificationPrefsSchema)
  .handler(async ({ data, context }) => {
    const { pollId, ...prefs } = data
    await pollService.updateNotificationPrefs(getDb(), pollId, context.session.user.id, prefs)
  })
