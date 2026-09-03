import { useCallback, useMemo, useState } from 'react'
import { toast } from 'sonner'
import * as z from 'zod'
import { useCaptchaEnabled } from '#/lib/captcha'
import { celebrate } from '#/lib/confetti'
import { saveEditToken } from '#/lib/edit-tokens'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import type { Answer } from '#/lib/scoring'
import type { Session } from '#/lib/use-session'
import { addParticipant, updateParticipant } from '#/api/polls'
import type { ParticipantView, PollView } from '#/api/types'

/** Maps a server error to the sentence that actually helps the person in front of the grid. */
function messageForError(error: unknown): string {
  switch (errorCode(error)) {
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
 * One answer-in-progress, shared by both layouts.
 *
 * The grid (wide screens) and the date list (phones) are two different renderings of the same
 * draft, so the draft cannot live inside either of them: they are both mounted at once and only
 * one is shown, and a visitor who rotates their phone mid-vote must not lose what they typed.
 */
export type AnswerDraft = {
  name: string
  setName: (name: string) => void
  email: string
  setEmail: (email: string) => void
  answers: Record<string, Answer>
  setAnswer: (optionId: string, answer: Answer | null) => void
  captchaToken: string | null
  setCaptchaToken: (token: string | null) => void
  submitting: boolean
  submit: () => Promise<void>
  /** Editing an existing row rather than adding one — no email or captcha is asked for again. */
  isEditing: boolean
  needsCaptcha: boolean
  requireEmail: boolean
  /** How many options have an answer, for the phone layout's progress line. */
  answeredCount: number
}

export function useAnswerDraft({
  poll,
  session,
  existingParticipant,
  guestToken,
  onSaved,
}: {
  poll: PollView
  session: Session
  existingParticipant?: ParticipantView
  guestToken?: string | null
  onSaved: () => void | Promise<void>
}): AnswerDraft {
  const captchaEnabled = useCaptchaEnabled()
  const [name, setName] = useState(existingParticipant?.name ?? session?.user.name ?? '')
  const [email, setEmail] = useState('')
  const [answers, setAnswers] = useState<Record<string, Answer>>(
    () => existingParticipant?.votes ?? {},
  )
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  // Adjusted during render rather than in an effect, the same pattern as `CapacityField`: starting
  // to edit a row (or backing out of one) has to refill the draft from that row before anything is
  // painted, and this used to be a remount of the whole add-yourself row via `key`.
  const editingId = existingParticipant?.id ?? null
  const [syncedId, setSyncedId] = useState(editingId)
  if (editingId !== syncedId) {
    setSyncedId(editingId)
    setName(existingParticipant?.name ?? session?.user.name ?? '')
    setAnswers(existingParticipant?.votes ?? {})
    setEmail('')
  }

  const isEditing = existingParticipant !== undefined
  const isGuest = session === null
  const needsCaptcha = isGuest && !isEditing && captchaEnabled
  const requireEmail = poll.settings.requireParticipantEmail && !isEditing

  const setAnswer = useCallback((optionId: string, answer: Answer | null) => {
    setAnswers((current) => {
      const next = { ...current }
      if (answer === null) delete next[optionId]
      else next[optionId] = answer
      return next
    })
  }, [])

  const answeredCount = useMemo(
    () => poll.options.filter((option) => answers[option.id] !== undefined).length,
    [answers, poll.options],
  )

  const submit = useCallback(async () => {
    const trimmedName = name.trim()
    if (!trimmedName) {
      toast.error(m.poll_error_name_required())
      return
    }
    const trimmedEmail = email.trim()
    if (requireEmail && !trimmedEmail) {
      toast.error(m.poll_error_email_required())
      return
    }
    if (trimmedEmail && !z.email().safeParse(trimmedEmail).success) {
      toast.error(m.poll_error_email_invalid())
      return
    }
    if (needsCaptcha && !captchaToken) {
      toast.error(m.poll_error_captcha())
      return
    }

    setSubmitting(true)
    try {
      if (existingParticipant) {
        await updateParticipant(
          poll.id,
          existingParticipant.id,
          { name: trimmedName, answers },
          { guestToken: guestToken ?? undefined },
        )
        toast.success(m.poll_vote_updated())
      } else {
        const result = await addParticipant(
          poll.id,
          { name: trimmedName, email: trimmedEmail || undefined, answers },
          { captchaToken: captchaToken ?? undefined },
        )
        if (result.guestToken) {
          saveEditToken(poll.id, result.participantId, result.guestToken)
        }
        celebrate('vote')
        toast.success(m.poll_vote_saved())
        setEmail('')
        setAnswers({})
        if (!session) setName('')
      }
      await onSaved()
    } catch (error) {
      toast.error(messageForError(error))
    } finally {
      setSubmitting(false)
    }
  }, [
    answers,
    captchaToken,
    email,
    existingParticipant,
    guestToken,
    name,
    needsCaptcha,
    onSaved,
    poll.id,
    requireEmail,
    session,
  ])

  return {
    name,
    setName,
    email,
    setEmail,
    answers,
    setAnswer,
    captchaToken,
    setCaptchaToken,
    submitting,
    submit,
    isEditing,
    needsCaptcha,
    requireEmail,
    answeredCount,
  }
}
