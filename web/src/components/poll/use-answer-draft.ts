import { useCallback, useMemo, useState } from 'react'
import { useServerFn } from '@tanstack/react-start'
import { toast } from 'sonner'
import * as z from 'zod'
import { celebrate } from '#/lib/confetti'
import { saveEditToken } from '#/lib/edit-tokens'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import type { Answer } from '#/lib/scoring'
import type { ClientSession } from '#/server/auth/session.functions'
import { addParticipant, updateParticipant } from '#/server/polls/participants.functions'
import type { ParticipantView, PollView } from '#/server/polls/viewmodel'

/** Maps a server error to the sentence that actually helps the person in front of the grid. */
function messageForError(error: unknown): string {
  switch (errorCode(error)) {
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
  editToken,
  onSaved,
}: {
  poll: PollView
  session: ClientSession
  existingParticipant?: ParticipantView
  editToken?: string | null
  onSaved: () => void | Promise<void>
}): AnswerDraft {
  const addFn = useServerFn(addParticipant)
  const updateFn = useServerFn(updateParticipant)

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
  const needsCaptcha = isGuest && !isEditing
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
        await updateFn({
          data: {
            pollId: poll.id,
            participantId: existingParticipant.id,
            editToken: editToken ?? undefined,
            name: trimmedName,
            answers,
          },
        })
        toast.success(m.poll_vote_updated())
      } else {
        const result = await addFn({
          data: {
            pollId: poll.id,
            name: trimmedName,
            email: trimmedEmail || undefined,
            answers,
            turnstileToken: captchaToken ?? undefined,
          },
        })
        if (result.editToken) {
          saveEditToken(poll.id, result.participantId, result.editToken)
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
    addFn,
    answers,
    captchaToken,
    editToken,
    email,
    existingParticipant,
    name,
    needsCaptcha,
    onSaved,
    poll.id,
    requireEmail,
    session,
    updateFn,
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
