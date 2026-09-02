import { useCallback, useMemo, useState } from 'react'
import { useServerFn } from '@tanstack/react-start'
import { toast } from 'sonner'
import { celebrate } from '#/lib/confetti'
import { saveEditToken, useEditToken } from '#/lib/edit-tokens'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import type { ClientSession } from '#/server/auth/session.functions'
import { claimSlot, unclaimSlot } from '#/server/polls/participants.functions'
import type { PollView } from '#/server/polls/viewmodel'

/** What a first-time claimer types into the identity sheet. */
export type ClaimIdentity = {
  name: string
  email?: string | undefined
  turnstileToken?: string | undefined
}

export type ClaimsViewer = {
  participantId: string | null
  name: string
  isOwner: boolean
  claimedOptionIds: string[]
  canClaimMore: boolean
}

export type UseClaims = {
  viewer: ClaimsViewer
  claim: (optionId: string, identity?: ClaimIdentity) => Promise<boolean>
  unclaim: (optionId: string, participantId?: string) => Promise<boolean>
  pending: ReadonlySet<string>
  needsIdentity: boolean
}

/** The sentence that actually helps someone whose claim just bounced. */
export function messageForClaimError(error: unknown): string {
  switch (errorCode(error)) {
    case 'SLOT_FULL':
      return m.signup_error_slot_full()
    case 'CLAIM_LIMIT_REACHED':
      return m.signup_error_limit()
    case 'POLL_CLOSED':
      return m.poll_error_closed()
    case 'EMAIL_REQUIRED':
      return m.poll_error_email_required()
    case 'CAPTCHA_FAILED':
      return m.poll_error_captcha()
    case 'RATE_LIMITED':
      return m.error_rate_limited()
    case 'LIMIT_REACHED':
      return m.poll_error_limit()
    case 'FORBIDDEN':
    case 'UNAUTHORIZED':
      return m.poll_error_forbidden()
    default:
      return m.poll_error_generic()
  }
}

/**
 * Claiming and releasing slots on a sign-up sheet, plus everything the board needs to know about
 * who is looking at it.
 *
 * Identity comes from the session (a signed-in participant's row) or from the edit token this
 * browser stored the first time it claimed something. Neither exists during SSR — `useEditToken`
 * is an external store that reads `null` on the server — so `needsIdentity` starts true and the
 * board only asks for a name once someone actually presses a button.
 *
 * Every mutation is optimistic in exactly one way: the slot it touches goes `pending` so its
 * button can't be double-fired. The real state comes back from `onChanged` (a route invalidation)
 * and, for everyone else on the page, from the poll room's `poll.changed` broadcast.
 */
export function useClaims(
  poll: PollView,
  session: ClientSession,
  onChanged: () => void | Promise<void>,
): UseClaims {
  const claimFn = useServerFn(claimSlot)
  const unclaimFn = useServerFn(unclaimSlot)
  const storedToken = useEditToken(poll.id)
  const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set())

  const you = useMemo(
    () =>
      poll.participants.find(
        (participant) =>
          (session !== null && participant.userId === session.user.id) ||
          participant.id === storedToken?.participantId,
      ),
    [poll.participants, session, storedToken],
  )

  const claimedOptionIds = useMemo(
    () =>
      you === undefined
        ? []
        : Object.entries(you.votes)
            .filter(([, answer]) => answer === 'yes')
            .map(([optionId]) => optionId),
    [you],
  )

  const viewer: ClaimsViewer = {
    participantId: you?.id ?? null,
    name: you?.name ?? session?.user.name ?? '',
    isOwner: poll.isOwner,
    claimedOptionIds,
    canClaimMore: claimedOptionIds.length < poll.settings.signupMaxClaims,
  }

  const markPending = useCallback((optionId: string, on: boolean) => {
    setPending((current) => {
      if (current.has(optionId) === on) return current
      const next = new Set(current)
      if (on) next.add(optionId)
      else next.delete(optionId)
      return next
    })
  }, [])

  const claim = useCallback(
    async (optionId: string, identity?: ClaimIdentity): Promise<boolean> => {
      if (pending.has(optionId)) return false
      // Someone with no row yet must go through the identity sheet first.
      if (you === undefined && identity === undefined) return false

      markPending(optionId, true)
      try {
        const firstClaim = claimedOptionIds.length === 0
        const result = await claimFn({
          data:
            you !== undefined
              ? {
                  pollId: poll.id,
                  optionId,
                  participantId: you.id,
                  editToken: storedToken?.token ?? undefined,
                }
              : {
                  pollId: poll.id,
                  optionId,
                  name: identity?.name,
                  email: identity?.email,
                  turnstileToken: identity?.turnstileToken,
                },
        })
        if (result.editToken) saveEditToken(poll.id, result.participantId, result.editToken)
        if (firstClaim) celebrate('vote')
        toast.success(m.signup_claimed_toast())
        await onChanged()
        return true
      } catch (error) {
        toast.error(messageForClaimError(error))
        return false
      } finally {
        markPending(optionId, false)
      }
    },
    [claimFn, claimedOptionIds, markPending, onChanged, pending, poll.id, storedToken, you],
  )

  const unclaim = useCallback(
    async (optionId: string, participantId?: string): Promise<boolean> => {
      const target = participantId ?? you?.id
      if (target === undefined || pending.has(optionId)) return false

      markPending(optionId, true)
      try {
        await unclaimFn({
          data: {
            pollId: poll.id,
            optionId,
            participantId: target,
            // The stored token only proves who *this* browser is; an owner freeing someone else's
            // spot is authorized by their session instead.
            editToken:
              target === storedToken?.participantId ? (storedToken?.token ?? undefined) : undefined,
          },
        })
        toast.success(target === you?.id ? m.signup_left_toast() : m.signup_removed_toast())
        await onChanged()
        return true
      } catch (error) {
        toast.error(messageForClaimError(error))
        return false
      } finally {
        markPending(optionId, false)
      }
    },
    [markPending, onChanged, pending, poll.id, storedToken, unclaimFn, you],
  )

  return { viewer, claim, unclaim, pending, needsIdentity: you === undefined }
}
