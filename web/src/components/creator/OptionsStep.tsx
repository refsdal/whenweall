import { useState, type Dispatch } from 'react'
import { CalendarDays, ListChecks } from 'lucide-react'
import { Badge } from '#/components/ui/badge'
import { DateOptionsEditor } from '#/components/creator/DateOptionsEditor'
import { TextOptionsEditor } from '#/components/creator/TextOptionsEditor'
import {
  countOptions,
  type CreatorAction,
  type CreatorDraft,
} from '#/components/creator/creator-state'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { LIMITS } from '#/api/polls'

export function optionCountLabel(count: number): string {
  return count === 1 ? m.creator_option_count_one() : m.creator_option_count_other({ count })
}

const SIGNUP_KINDS = [
  { kind: 'dates' as const, icon: CalendarDays, label: () => m.creator_signup_kind_dates() },
  { kind: 'text' as const, icon: ListChecks, label: () => m.creator_signup_kind_text() },
]

/** Step 2: the things people will vote on or sign up for — days and times, or a list of choices. */
export function OptionsStep({
  draft,
  dispatch,
}: {
  draft: CreatorDraft
  dispatch: Dispatch<CreatorAction>
}) {
  const count = countOptions(draft)
  const overLimit = count > LIMITS.options
  const isSignup = draft.type === 'signup'

  // Only meaningful for signup, which (unlike `datetime`/`options`) can use either kind of
  // option: a segmented toggle picks the editor, defaulting to whichever one the draft already
  // has content in. Kept as local state (not on the draft) since it is purely presentational —
  // `usesTextOptions` in creator-state.ts derives the real answer from `textOptions.length`.
  const [signupKind, setSignupKind] = useState<'dates' | 'text'>(
    draft.textOptions.length > 0 ? 'text' : 'dates',
  )

  function chooseSignupKind(kind: 'dates' | 'text') {
    if (kind === signupKind) return
    setSignupKind(kind)
    if (kind === 'dates' && draft.textOptions.length > 0) {
      dispatch({ type: 'setTextOptions', options: [] })
    }
    if (kind === 'text' && draft.dates.length > 0) {
      dispatch({ type: 'setField', field: 'dates', value: [] })
    }
  }

  const showTextEditor = draft.type === 'options' || (isSignup && signupKind === 'text')

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <div>
          <h2 className="font-medium">
            {isSignup
              ? m.creator_signup_options_title()
              : draft.type === 'datetime'
                ? m.creator_dates_title()
                : m.creator_text_title()}
          </h2>
          <p className="text-sm text-muted-foreground">
            {isSignup
              ? m.creator_signup_options_hint()
              : draft.type === 'datetime'
                ? m.creator_dates_hint()
                : m.creator_text_hint()}
          </p>
        </div>
        <Badge variant={count > 0 && !overLimit ? 'soft' : 'secondary'} className="tabular-nums">
          {optionCountLabel(count)}
        </Badge>
      </div>

      {isSignup && (
        <fieldset className="flex gap-2">
          <legend className="sr-only">{m.creator_signup_kind_legend()}</legend>
          {SIGNUP_KINDS.map(({ kind, icon: Icon, label }) => {
            const selected = signupKind === kind
            return (
              <button
                key={kind}
                type="button"
                aria-pressed={selected}
                onClick={() => chooseSignupKind(kind)}
                className={cn(
                  'focus-ring inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-sm font-medium transition-colors',
                  selected
                    ? 'border-primary bg-accent-soft text-accent-foreground'
                    : 'border-border text-muted-foreground hover:text-foreground',
                )}
              >
                <Icon aria-hidden="true" className="size-3.5" />
                {label()}
              </button>
            )
          })}
        </fieldset>
      )}

      {showTextEditor ? (
        <TextOptionsEditor
          value={draft.textOptions}
          onChange={(options) => dispatch({ type: 'setTextOptions', options })}
          showCapacity={isSignup}
        />
      ) : (
        <DateOptionsEditor draft={draft} dispatch={dispatch} />
      )}

      {overLimit && (
        <p role="alert" className="text-sm text-destructive">
          {m.creator_options_limit({ max: LIMITS.options })}
        </p>
      )}
    </div>
  )
}
