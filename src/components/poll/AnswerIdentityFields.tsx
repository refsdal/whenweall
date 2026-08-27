import { Input } from '#/components/ui/input'
import { m } from '#/lib/i18n'
import type { AnswerDraft } from '#/components/poll/use-answer-draft'

/**
 * Who you are and where to reach you, on a phone.
 *
 * These sit above the list of dates: you say who you are, then work down the rows. Below the list
 * they were easy to miss — the sticky save bar covers the bottom of the viewport, so the fields
 * only came into view after scrolling past every date, and the save button was reachable long
 * before the name it needed.
 *
 * Phone-only (`sm:hidden`). On a wide screen the name belongs in the grid's own row, under the
 * "Who" column (`AddYourselfRow`), and the email stays in `AnswerForm` below the grid — so this
 * duplicates neither, it renders the pair the phone layout would otherwise not have. Duplicate
 * text inputs across the two layouts are harmless in the way the Turnstile widget is not (see
 * `AnswerForm`): only one is ever displayed, and `display: none` keeps the other out of the
 * accessibility tree.
 */
export function AnswerIdentityFields({ draft }: { draft: AnswerDraft }) {
  return (
    <div className="flex flex-col gap-3 rounded-xl border border-border bg-accent-soft/30 p-4 sm:hidden">
      <div className="flex flex-col gap-1.5">
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
        <div className="flex flex-col gap-1">
          <Input
            type="email"
            value={draft.email}
            onChange={(event) => draft.setEmail(event.target.value)}
            autoComplete="email"
            aria-label={m.poll_email_label()}
            placeholder={m.poll_email_label()}
            required={draft.requireEmail}
            className="h-11"
          />
          <p className="text-xs text-muted-foreground">
            {draft.requireEmail ? m.poll_email_hint_required() : m.poll_email_hint_optional()}
          </p>
        </div>
      )}
    </div>
  )
}
