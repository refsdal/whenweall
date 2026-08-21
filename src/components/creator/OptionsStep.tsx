import type { Dispatch } from 'react'
import { Badge } from '#/components/ui/badge'
import { DateOptionsEditor } from '#/components/creator/DateOptionsEditor'
import { TextOptionsEditor } from '#/components/creator/TextOptionsEditor'
import {
  countOptions,
  type CreatorAction,
  type CreatorDraft,
} from '#/components/creator/creator-state'
import { m } from '#/lib/i18n'
import { LIMITS } from '#/server/polls/schemas'

export function optionCountLabel(count: number): string {
  return count === 1 ? m.creator_option_count_one() : m.creator_option_count_other({ count })
}

/** Step 2: the things people will vote on — days and times, or a list of choices. */
export function OptionsStep({
  draft,
  dispatch,
}: {
  draft: CreatorDraft
  dispatch: Dispatch<CreatorAction>
}) {
  const count = countOptions(draft)
  const overLimit = count > LIMITS.options

  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <div>
          <h2 className="font-medium">
            {draft.type === 'datetime' ? m.creator_dates_title() : m.creator_text_title()}
          </h2>
          <p className="text-sm text-muted-foreground">
            {draft.type === 'datetime' ? m.creator_dates_hint() : m.creator_text_hint()}
          </p>
        </div>
        <Badge variant={count > 0 && !overLimit ? 'soft' : 'secondary'} className="tabular-nums">
          {optionCountLabel(count)}
        </Badge>
      </div>

      {draft.type === 'datetime' ? (
        <DateOptionsEditor draft={draft} dispatch={dispatch} />
      ) : (
        <TextOptionsEditor
          value={draft.textOptions}
          onChange={(options) => dispatch({ type: 'setTextOptions', options })}
        />
      )}

      {overLimit && (
        <p role="alert" className="text-sm text-destructive">
          {m.creator_options_limit({ max: LIMITS.options })}
        </p>
      )}
    </div>
  )
}
