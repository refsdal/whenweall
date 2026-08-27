import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import type { AnswerDraft } from '#/components/poll/use-answer-draft'
import type { PollView } from '#/server/polls/viewmodel'

/**
 * Everything about an answer that is not per-option: who you are, where to reach you, and the
 * button that saves it.
 *
 * Rendered exactly once per page, below both layouts. It has to be once rather than per-layout:
 * the grid and the date list are both mounted (only one is shown), and a second Turnstile widget
 * would solve into the same draft and overwrite a good token with its own — or with `null` when
 * the hidden copy fails, which it does, being hidden.
 *
 * The name field is the one thing that moves: on a wide screen it belongs in the grid row, where
 * it lines up under the "Who" column, so this form only shows it on a phone.
 */
export function AnswerForm({
  poll,
  draft,
  onCancel,
  showSaveBar,
}: {
  poll: PollView
  draft: AnswerDraft
  /** Present while editing an existing row, which can be backed out of. */
  onCancel?: () => void
  /**
   * Whether the phone layout gets the sticky save bar. Off for an organiser, whose `AdminBar` is
   * already pinned to the bottom of the viewport — two bars would sit on top of each other.
   */
  showSaveBar: boolean
}) {
  const saveLabel = draft.submitting
    ? m.poll_saving()
    : draft.isEditing
      ? m.poll_update_answer()
      : m.poll_save_answer()

  return (
    <>
      <div className="flex flex-col gap-3 rounded-xl border border-border bg-accent-soft/30 p-4">
        <div className="flex flex-col gap-1.5 sm:hidden">
          <label htmlFor="poll-your-name" className="text-sm font-medium">
            {m.poll_your_name_label()}
          </label>
          <Input
            id="poll-your-name"
            value={draft.name}
            onChange={(event) => draft.setName(event.target.value)}
            maxLength={80}
            autoComplete="name"
            placeholder={m.poll_your_name_placeholder()}
            className="h-11"
          />
        </div>

        {!draft.isEditing && (
          <div className="flex flex-col gap-1 sm:max-w-sm">
            <Input
              type="email"
              value={draft.email}
              onChange={(event) => draft.setEmail(event.target.value)}
              autoComplete="email"
              aria-label={m.poll_email_label()}
              placeholder={m.poll_email_label()}
              required={draft.requireEmail}
              className="h-11 sm:h-9"
            />
            <p className="text-xs text-muted-foreground">
              {draft.requireEmail ? m.poll_email_hint_required() : m.poll_email_hint_optional()}
            </p>
          </div>
        )}

        {draft.needsCaptcha && <TurnstileField onToken={draft.setCaptchaToken} />}

        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            onClick={() => void draft.submit()}
            disabled={draft.submitting}
            className={cn(showSaveBar && 'max-sm:hidden')}
          >
            {saveLabel}
          </Button>
          {onCancel && (
            <Button type="button" variant="ghost" onClick={onCancel} disabled={draft.submitting}>
              {m.common_cancel()}
            </Button>
          )}
          <p className="text-xs text-muted-foreground max-sm:hidden">{m.poll_vote_cell_hint()}</p>
        </div>
      </div>

      {showSaveBar && (
        <div
          data-testid="answer-save-bar"
          className="sticky bottom-0 z-20 -mx-5 flex items-center gap-3 border-t border-border bg-card/95 px-5 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur supports-[backdrop-filter]:bg-card/80 sm:hidden"
        >
          <span className="flex-1 text-sm tabular-nums text-muted-foreground">
            {m.poll_list_answered({
              count: draft.answeredCount,
              total: poll.options.length,
            })}
          </span>
          <Button
            type="button"
            size="lg"
            onClick={() => void draft.submit()}
            disabled={draft.submitting}
          >
            {saveLabel}
          </Button>
        </div>
      )}
    </>
  )
}
