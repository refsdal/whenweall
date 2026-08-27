import { useEffect, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { VoteCell } from '#/components/poll/VoteCell'
import { Input } from '#/components/ui/input'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { nextAnswer, type Answer } from '#/lib/scoring'
import type { AnswerDraft } from '#/components/poll/use-answer-draft'
import type { PollView } from '#/server/polls/viewmodel'

/**
 * The row you fill in yourself: your name, and a cell per option. It lives inside the grid's
 * `<tbody>` so its cells line up with everybody else's, which is what makes voting feel like
 * writing on the same sheet of paper.
 *
 * Everything that is not per-option — the email, the captcha, the save button — lives in
 * `AnswerForm` below the grid instead, because the phone layout needs the same fields and two
 * mounted captchas would race to overwrite one another's token.
 *
 * Editing an existing answer reuses the same row: the participant's row is swapped out for this
 * one, pre-filled by the draft.
 */
export function AddYourselfRow({
  poll,
  optionLabels,
  draft,
}: {
  poll: PollView
  optionLabels: Record<string, string>
  draft: AnswerDraft
}) {
  // `null` means "not painting" — the answer being painted may itself legitimately be null.
  const [painting, setPainting] = useState<{ answer: Answer | null } | null>(null)

  // The drag ends wherever the pointer happens to be, which is often outside the grid entirely.
  useEffect(() => {
    if (painting === null) return
    const stop = () => setPainting(null)
    window.addEventListener('pointerup', stop)
    window.addEventListener('pointercancel', stop)
    return () => {
      window.removeEventListener('pointerup', stop)
      window.removeEventListener('pointercancel', stop)
    }
  }, [painting])

  /**
   * Press a cell and drag along the row to give every cell you cross the same answer — eight
   * options otherwise means eight taps to say "no to all of these".
   *
   * The answer being painted is whatever the cell you started on is about to become, so a press
   * that turns into a drag continues the answer the press already chose; the origin cell is left
   * to its own click handler.
   *
   * Mouse and pen only. Painting on touch needs `touch-action: none` on every cell to stop the
   * browser claiming the gesture, which would cost a visitor the ability to scroll the page from
   * the one part of it they spend the most time on. Tapping still cycles a cell as before.
   */
  function startPaint(event: ReactPointerEvent, optionId: string) {
    if (event.pointerType === 'touch' || event.button !== 0) return
    setPainting({
      answer: nextAnswer(draft.answers[optionId] ?? null, poll.settings.allowIfNeedBe),
    })
  }

  function paintOver(optionId: string) {
    if (painting === null) return
    draft.setAnswer(optionId, painting.answer)
  }

  return (
    <tr data-testid="add-yourself-row" className="bg-accent-soft/30">
      <th scope="row" className="sticky left-0 z-10 border-t border-border bg-card px-3 py-2">
        <Input
          value={draft.name}
          onChange={(event) => draft.setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              void draft.submit()
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
          data-option-id={option.id}
          data-best={option.id === poll.bestOptionId ? 'true' : undefined}
          onPointerDown={(event) => startPaint(event, option.id)}
          onPointerEnter={() => paintOver(option.id)}
          className={cn(
            'border-t border-border px-1 py-1.5 select-none',
            option.id === poll.bestOptionId && 'bg-accent-soft/35',
          )}
        >
          <VoteCell
            answer={draft.answers[option.id] ?? null}
            onChange={(answer) => draft.setAnswer(option.id, answer)}
            allowIfNeedBe={poll.settings.allowIfNeedBe}
            optionLabel={optionLabels[option.id]}
          />
        </td>
      ))}
    </tr>
  )
}
