import { useState } from 'react'
import { useServerFn } from '@tanstack/react-start'
import { toast } from 'sonner'
import * as z from 'zod'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { VoteCell } from '#/components/poll/VoteCell'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { celebrate } from '#/lib/confetti'
import { saveEditToken } from '#/lib/edit-tokens'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
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
 * The row you fill in yourself: your name, a cell per option, and — for guests — an email and a
 * captcha. It lives inside the grid's `<tbody>` so its cells line up with everybody else's, which
 * is what makes voting feel like writing on the same sheet of paper.
 *
 * Editing an existing answer reuses the same row: the participant's row is swapped out for this
 * one, pre-filled.
 */
export function AddYourselfRow({
  poll,
  session,
  optionLabels,
  existingParticipant,
  editToken,
  onSaved,
  onCancel,
}: {
  poll: PollView
  session: ClientSession
  optionLabels: Record<string, string>
  existingParticipant?: ParticipantView
  editToken?: string | null
  onSaved: () => void | Promise<void>
  onCancel?: () => void
}) {
  const addFn = useServerFn(addParticipant)
  const updateFn = useServerFn(updateParticipant)

  const [name, setName] = useState(existingParticipant?.name ?? session?.user.name ?? '')
  const [email, setEmail] = useState('')
  const [answers, setAnswers] = useState<Record<string, Answer>>(
    () => existingParticipant?.votes ?? {},
  )
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const isEditing = existingParticipant !== undefined
  const isGuest = session === null
  const needsCaptcha = isGuest && !isEditing
  const requireEmail = poll.settings.requireParticipantEmail && !isEditing
  const columnCount = poll.options.length + 1

  function setAnswer(optionId: string, answer: Answer | null) {
    setAnswers((current) => {
      const next = { ...current }
      if (answer === null) delete next[optionId]
      else next[optionId] = answer
      return next
    })
  }

  async function submit() {
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
  }

  return (
    <>
      <tr data-testid="add-yourself-row" className="bg-accent-soft/30">
        <th scope="row" className="sticky left-0 z-10 border-t border-border bg-card px-3 py-2">
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                void submit()
              }
            }}
            maxLength={80}
            autoComplete="name"
            aria-label={m.poll_your_name_label()}
            placeholder={m.poll_your_name_placeholder()}
            className="h-9"
          />
        </th>
        {poll.options.map((option) => (
          <td
            key={option.id}
            data-best={option.id === poll.bestOptionId ? 'true' : undefined}
            className={cn(
              'border-t border-border px-1 py-1.5',
              option.id === poll.bestOptionId && 'bg-accent-soft/35',
            )}
          >
            <VoteCell
              answer={answers[option.id] ?? null}
              onChange={(answer) => setAnswer(option.id, answer)}
              allowIfNeedBe={poll.settings.allowIfNeedBe}
              optionLabel={optionLabels[option.id]}
            />
          </td>
        ))}
      </tr>

      <tr className="bg-accent-soft/30">
        <td colSpan={columnCount} className="px-3 pt-1 pb-3">
          <div className="sticky left-0 flex max-w-[min(100%,44rem)] flex-col gap-3">
            {!isEditing && (
              <div className="flex flex-col gap-1 sm:max-w-sm">
                <Input
                  type="email"
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  autoComplete="email"
                  aria-label={m.poll_email_label()}
                  placeholder={m.poll_email_label()}
                  required={requireEmail}
                  className="h-9"
                />
                <p className="text-xs text-muted-foreground">
                  {requireEmail ? m.poll_email_hint_required() : m.poll_email_hint_optional()}
                </p>
              </div>
            )}

            {needsCaptcha && <TurnstileField onToken={setCaptchaToken} />}

            <div className="flex flex-wrap items-center gap-2">
              <Button type="button" onClick={() => void submit()} disabled={submitting}>
                {submitting
                  ? m.poll_saving()
                  : isEditing
                    ? m.poll_update_answer()
                    : m.poll_save_answer()}
              </Button>
              {onCancel && (
                <Button type="button" variant="ghost" onClick={onCancel} disabled={submitting}>
                  {m.common_cancel()}
                </Button>
              )}
              <p className="text-xs text-muted-foreground">{m.poll_vote_cell_hint()}</p>
            </div>
          </div>
        </td>
      </tr>
    </>
  )
}
