import * as z from 'zod'

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

export const pollIdSchema = z.string().regex(/^[0-9A-Za-z]{12}$/)

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

export const pollSettingsSchema = z.object({
  requireParticipantEmail: z.boolean(),
  allowComments: z.boolean(),
  allowIfNeedBe: z.boolean(),
})

/**
 * Shared refinement logic for a poll's options, used by both createPollSchema
 * (where `type` is required) and updatePollSchema (where `type` and `options`
 * are both optional, but must agree when both are present).
 */
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
      ctx.addIssue({
        code: 'custom',
        message: 'Duplicate option',
        path: ['options', index],
      })
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
  .extend({ pollId: pollIdSchema })
  .superRefine((data, ctx) => {
    refinePollOptions(undefined, data.options, ctx)
  })

export type UpdatePollInput = z.infer<typeof updatePollSchema>

export const addParticipantSchema = z.object({
  pollId: pollIdSchema,
  name: z.string().trim().min(1).max(LIMITS.name),
  email: z.union([z.literal(''), z.email().max(254)]).optional(),
  answers: z.record(z.string(), answerSchema),
  turnstileToken: z.string().optional(),
})

export type AddParticipantInput = z.infer<typeof addParticipantSchema>

export const updateParticipantSchema = z.object({
  pollId: pollIdSchema,
  participantId: z.string(),
  editToken: z.string().optional(),
  name: z.string().trim().min(1).max(LIMITS.name).optional(),
  answers: z.record(z.string(), answerSchema),
})

export type UpdateParticipantInput = z.infer<typeof updateParticipantSchema>

export const addCommentSchema = z.object({
  pollId: pollIdSchema,
  authorName: z.string().trim().min(1).max(LIMITS.name),
  body: z.string().trim().min(1).max(LIMITS.comment),
  turnstileToken: z.string().optional(),
  participantId: z.string().optional(),
  editToken: z.string().optional(),
})

export type AddCommentInput = z.infer<typeof addCommentSchema>

export const notificationPrefsSchema = z.object({
  pollId: pollIdSchema,
  notifyOnVote: z.boolean(),
  notifyOnComment: z.boolean(),
})

export type NotificationPrefsInput = z.infer<typeof notificationPrefsSchema>

export const claimSchema = z.object({
  pollId: pollIdSchema,
  optionId: z.string(),
  participantId: z.string().optional(),
  editToken: z.string().optional(),
  name: z.string().trim().min(1).max(LIMITS.name).optional(),
  email: z.union([z.literal(''), z.email().max(254)]).optional(),
  turnstileToken: z.string().optional(),
})

export type ClaimInput = z.infer<typeof claimSchema>

export const unclaimSchema = z.object({
  pollId: pollIdSchema,
  optionId: z.string(),
  participantId: z.string(),
  editToken: z.string().optional(),
})

export type UnclaimInput = z.infer<typeof unclaimSchema>
