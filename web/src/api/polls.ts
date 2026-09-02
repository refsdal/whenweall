import * as z from 'zod'
import { api } from '#/api/client'
import type { NotificationGrid } from '#/lib/notifications'
import type { PollSummary, PollView } from '#/api/types'

/**
 * Per-module functions mirroring the old `polls.functions.ts`/`participants.functions.ts` names
 * 1:1, against internal/polls/handlers.go's REST surface. Request/response shapes come from that
 * file's DTOs (createPollRequest, addParticipantRequest, claimRequest, ...), NOT from the old TS
 * server code — see this task's report for the full mapping.
 */

export const LIMITS = {
  title: 200,
  description: 2000,
  location: 200,
  options: 100,
  participants: 500,
  name: 80,
  comment: 2000,
  optionLabel: 100,
} as const

// ---- client-side validation (a cheap pre-check before the round trip; the Go backend is the
// real source of truth and validates again) --------------------------------------------------

export const answerSchema = z.enum(['yes', 'ifneedbe', 'no'])
export const capacitySchema = z.number().int().min(1).max(10000).nullable()

export const optionInputSchema = z.discriminatedUnion('kind', [
  z.object({
    id: z.string().optional(),
    kind: z.literal('date'),
    date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/),
    capacity: capacitySchema.optional(),
  }),
  z.object({
    id: z.string().optional(),
    kind: z.literal('datetime'),
    startAt: z.iso.datetime(),
    endAt: z.iso.datetime().nullable().optional(),
    capacity: capacitySchema.optional(),
  }),
  z.object({
    id: z.string().optional(),
    kind: z.literal('text'),
    label: z.string().trim().min(1).max(LIMITS.optionLabel),
    capacity: capacitySchema.optional(),
  }),
])

export type OptionInput = z.infer<typeof optionInputSchema>

const pollSettingsSchema = z.object({
  requireParticipantEmail: z.boolean(),
  allowComments: z.boolean(),
  allowIfNeedBe: z.boolean(),
})

function refinePollOptions(
  type: 'datetime' | 'options' | 'signup' | undefined,
  options: OptionInput[] | undefined,
  ctx: z.RefinementCtx,
): void {
  if (!options) return

  const seenKeys = new Set<string>()
  let signupKind: OptionInput['kind'] | undefined

  options.forEach((option, index) => {
    if (type === 'datetime' && option.kind !== 'date' && option.kind !== 'datetime') {
      ctx.addIssue({
        code: 'custom',
        message: 'Options for a datetime poll must be dates or date/times',
        path: ['options', index],
      })
    }
    if (type === 'options' && option.kind !== 'text') {
      ctx.addIssue({
        code: 'custom',
        message: 'Options for an options poll must be text',
        path: ['options', index],
      })
    }
    if (type === 'signup') {
      if (signupKind === undefined) {
        signupKind = option.kind
      } else if (option.kind !== signupKind) {
        ctx.addIssue({
          code: 'custom',
          message: 'Sign-up sheet options must all be the same kind',
          path: ['options', index],
        })
      }
    }
    if (
      type !== undefined &&
      type !== 'signup' &&
      option.capacity !== undefined &&
      option.capacity !== null
    ) {
      ctx.addIssue({
        code: 'custom',
        message: 'Capacity is only allowed on sign-up sheet options',
        path: ['options', index, 'capacity'],
      })
    }

    if (option.kind === 'datetime' && option.endAt) {
      if (new Date(option.endAt).getTime() <= new Date(option.startAt).getTime()) {
        ctx.addIssue({
          code: 'custom',
          message: 'endAt must be after startAt',
          path: ['options', index, 'endAt'],
        })
      }
    }

    const key =
      option.kind === 'date'
        ? `date:${option.date}`
        : option.kind === 'datetime'
          ? `datetime:${option.startAt}|${option.endAt ?? ''}`
          : `text:${option.label.trim().toLowerCase()}`

    if (seenKeys.has(key)) {
      ctx.addIssue({ code: 'custom', message: 'Duplicate option', path: ['options', index] })
    } else {
      seenKeys.add(key)
    }
  })
}

const createPollBase = z
  .object({
    type: z.enum(['datetime', 'options', 'signup']),
    title: z.string().trim().min(1).max(LIMITS.title),
    description: z.string().trim().max(LIMITS.description).optional(),
    location: z.string().trim().max(LIMITS.location).optional(),
    timezone: z.string().min(1).max(64),
    deadlineAt: z.iso.datetime().nullable().optional(),
    options: z.array(optionInputSchema).min(1).max(LIMITS.options),
    signupMaxClaims: z.number().int().min(1).max(100).optional(),
  })
  .extend(pollSettingsSchema.partial().shape)

export const createPollSchema = createPollBase.superRefine((data, ctx) => {
  refinePollOptions(data.type, data.options, ctx)
  if (data.type !== 'signup' && data.signupMaxClaims !== undefined) {
    ctx.addIssue({
      code: 'custom',
      message: 'signupMaxClaims is only allowed for sign-up sheets',
      path: ['signupMaxClaims'],
    })
  }
})

export type CreatePollInput = z.infer<typeof createPollSchema>

export const updatePollSchema = createPollBase
  .omit({ type: true })
  .partial()
  .extend({ pollId: z.string() })
  .superRefine((data, ctx) => {
    refinePollOptions(undefined, data.options, ctx)
  })

export type UpdatePollInput = z.infer<typeof updatePollSchema>

// ---- polls ------------------------------------------------------------------------------------

export function createPoll(input: CreatePollInput): Promise<PollView> {
  return api<PollView>('POST', '/api/v1/polls', input)
}

export function getPoll(pollId: string, guestToken?: string): Promise<PollView> {
  return api<PollView>('GET', `/api/v1/polls/${pollId}`, undefined, { guestToken })
}

export async function updatePoll(input: UpdatePollInput): Promise<void> {
  const { pollId, ...body } = input
  await api('PATCH', `/api/v1/polls/${pollId}`, body)
}

export async function setPollStatus(pollId: string, status: 'open' | 'closed'): Promise<PollView> {
  return api<PollView>('POST', `/api/v1/polls/${pollId}/status`, { status })
}

export function finalizePoll(pollId: string, optionId: string): Promise<{ sent: number }> {
  return api<{ sent: number }>('POST', `/api/v1/polls/${pollId}/finalize`, { optionId })
}

export async function deletePoll(pollId: string): Promise<void> {
  await api('DELETE', `/api/v1/polls/${pollId}`)
}

export function duplicatePoll(pollId: string): Promise<PollView> {
  return api<PollView>('POST', `/api/v1/polls/${pollId}/duplicate`)
}

export function listMyPolls(): Promise<PollSummary[]> {
  return api<PollSummary[]>('GET', '/api/v1/polls')
}

// ---- participants/comments/claims --------------------------------------------------------------

export type AddParticipantResult = { participantId: string; guestToken?: string }

export function addParticipant(
  pollId: string,
  input: { name: string; email?: string; answers: Record<string, string>; locale?: string },
  opts?: { captchaToken?: string },
): Promise<AddParticipantResult> {
  return api<AddParticipantResult>(
    'POST',
    `/api/v1/polls/${pollId}/participants`,
    { name: input.name, email: input.email, answers: input.answers, locale: input.locale },
    { captchaToken: opts?.captchaToken },
  )
}

export async function updateParticipant(
  pollId: string,
  participantId: string,
  input: { name?: string; answers: Record<string, string> },
  opts?: { guestToken?: string },
): Promise<void> {
  await api(
    'PATCH',
    `/api/v1/polls/${pollId}/participants/${participantId}`,
    input,
    { guestToken: opts?.guestToken },
  )
}

export async function removeParticipant(
  pollId: string,
  participantId: string,
  opts?: { guestToken?: string },
): Promise<void> {
  await api('DELETE', `/api/v1/polls/${pollId}/participants/${participantId}`, undefined, {
    guestToken: opts?.guestToken,
  })
}

export type AddCommentResult = {
  id: string
  authorName: string
  body: string
  createdAt: string
  userId: string | null
  participantId: string | null
}

export function addComment(
  pollId: string,
  input: { authorName: string; body: string },
  opts?: { captchaToken?: string; guestToken?: string },
): Promise<AddCommentResult> {
  return api<AddCommentResult>(
    'POST',
    `/api/v1/polls/${pollId}/comments`,
    { authorName: input.authorName, body: input.body },
    opts,
  )
}

export async function deleteComment(pollId: string, commentId: string): Promise<void> {
  await api('DELETE', `/api/v1/polls/${pollId}/comments/${commentId}`)
}

export type ClaimResult = { participantId: string; claimedOptionIds: string[]; guestToken?: string }

export function claimSlot(
  pollId: string,
  input: { optionId: string; participantId?: string; name?: string; email?: string },
  opts?: { captchaToken?: string; guestToken?: string },
): Promise<ClaimResult> {
  return api<ClaimResult>(
    'POST',
    `/api/v1/polls/${pollId}/claims`,
    {
      optionId: input.optionId,
      participantId: input.participantId,
      name: input.name,
      email: input.email,
    },
    opts,
  )
}

/** `forceParticipantId` is the manager force-unclaim path (`?participantId=`) — freeing a
 * different participant's slot. Omit it for the self-service path. */
export async function unclaimSlot(
  pollId: string,
  optionId: string,
  opts?: { guestToken?: string; forceParticipantId?: string },
): Promise<void> {
  await api(
    'DELETE',
    `/api/v1/polls/${pollId}/claims/${optionId}`,
    undefined,
    {
      guestToken: opts?.guestToken,
      query: opts?.forceParticipantId ? { participantId: opts.forceParticipantId } : undefined,
    },
  )
}

// ---- calendar/roster/notifications --------------------------------------------------------------

/** A plain link, not a fetch call — the browser downloads this directly (owner-session cookie
 * auth, same-origin). */
export function rosterCsvUrl(pollId: string): string {
  return `/api/v1/polls/${pollId}/roster.csv`
}

export function calendarIcsUrl(pollId: string): string {
  return `/api/v1/polls/${pollId}/calendar.ics`
}

export async function updateNotificationPrefs(
  pollId: string,
  channels: NotificationGrid | null,
): Promise<void> {
  await api('POST', `/api/v1/polls/${pollId}/notification-prefs`, { channels })
}

export async function setPollFollowing(pollId: string, following: boolean): Promise<void> {
  await api('POST', `/api/v1/polls/${pollId}/following`, { following })
}
