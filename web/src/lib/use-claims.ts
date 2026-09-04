import { useCallback, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { celebrate } from '#/lib/confetti'
import { saveEditToken, useEditToken } from '#/lib/edit-tokens'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import type { Session } from '#/lib/use-session'
import { claimSlot, unclaimSlot } from '#/api/polls'
import type { PollView } from '#/api/types'

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
    case 'capacity_full':
      return m.signup_error_slot_full()
    case 'claim_limit_reached':
      return m.signup_error_limit()
    case 'poll_closed':
      return m.poll_error_closed()
    case 'email_required':
      return m.poll_error_email_required()
    case 'captcha_failed':
      return m.poll_error_captcha()
    case 'rate_limited':
      return m.error_rate_limited()
    case 'limit_reached':
      return m.poll_error_limit()
    case 'forbidden':
    case 'unauthenticated':
      return m.poll_error_forbidden()
    default:
      return m.poll_error_generic()
  }
}

/**
 * Claiming and releasing slots on a sign-up sheet, plus everything the board needs to know about
 * who is looking at it.
 *
 * Identity comes from the session (a signed-in participant's row) or from the guest token this
 * browser stored the first time it claimed something (the Go backend's `X-Guest-Token` header —
 * the same `{participantId, token}` shape/storage `edit-tokens.ts` already had, just reinterpreted:
 * there is no separate "edit token" concept server-side anymore, only this one guest credential —
 * see the task report's code-mapping note). `needsIdentity` starts true and the board only asks
 * for a name once someone actually presses a button.
 *
 * Every mutation is optimistic in exactly one way: the slot it touches goes `pending` so its
 * button can't be double-fired. The real state comes back from `onChanged` (a route invalidation)
 * and, for everyone else on the page, from the poll room's `poll.changed` broadcast.
 */
export function useClaims(
  poll: PollView,
  session: Session,
  onChanged: () => void | Promise<void>,
): UseClaims {
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
        const result = await claimSlot(
          poll.id,
          you !== undefined
            ? { optionId, participantId: you.id }
            : { optionId, name: identity?.name, email: identity?.email },
          { guestToken: storedToken?.token, captchaToken: identity?.turnstileToken },
        )
        if (result.guestToken) saveEditToken(poll.id, result.participantId, result.guestToken)
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
    [claimedOptionIds, markPending, onChanged, pending, poll.id, storedToken, you],
  )

  const unclaim = useCallback(
    async (optionId: string, participantId?: string): Promise<boolean> => {
      const target = participantId ?? you?.id
      if (target === undefined || pending.has(optionId)) return false

      markPending(optionId, true)
      try {
        await unclaimSlot(poll.id, optionId, {
          // The stored token only proves who *this* browser is; an owner freeing someone else's
          // spot is authorized by their session instead.
          guestToken: target === storedToken?.participantId ? storedToken?.token : undefined,
          forceParticipantId: target !== you?.id ? target : undefined,
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
    [markPending, onChanged, pending, poll.id, storedToken, you],
  )

  return { viewer, claim, unclaim, pending, needsIdentity: you === undefined }
}
