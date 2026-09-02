import { BestBadge } from '#/components/poll/BestBadge'
import type { AppLocale } from '#/app.config'
import { formatOptionLabel } from '#/lib/time'
import { cn } from '#/lib/utils'
import type { PollOptionView } from '#/api/types'

/** The label a screen reader reads for a cell in this column, and the tooltip on the header. */
export function optionPlainLabel(
  option: PollOptionView,
  locale: AppLocale,
  timeZone: string,
): string {
  const label = formatOptionLabel(option, { locale, timeZone })
  return [label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')
}

/**
 * One column head: the date (or the option's text), in the viewer's own timezone, crowned when
 * it is winning or has been picked.
 */
export function OptionHeader({
  option,
  locale,
  timeZone,
  best,
  finalized,
  hovered = false,
}: {
  option: PollOptionView
  locale: AppLocale
  timeZone: string
  best: boolean
  finalized: boolean
  /** The pointer is somewhere in this column. */
  hovered?: boolean
}) {
  const label = formatOptionLabel(option, { locale, timeZone })
  const crowned = finalized || best

  return (
    <th
      scope="col"
      data-testid={`option-header-${option.id}`}
      data-option-id={option.id}
      data-best={best ? 'true' : undefined}
      data-finalized={finalized ? 'true' : undefined}
      title={optionPlainLabel(option, locale, timeZone)}
      className="px-1 pt-2 pb-1 align-bottom font-normal first-of-type:pl-0"
    >
      {/* A reserved strip above the label so a crown can never shift the header's height. */}
      <div className="flex h-4 items-end justify-center">
        {crowned && <BestBadge variant={finalized ? 'picked' : 'best'} />}
      </div>

      <div
        className={cn(
          'mt-1 flex min-w-[4.25rem] flex-col items-center gap-0.5 rounded-xl px-2 py-2 transition-[box-shadow,background-color] duration-300',
          best && !finalized && 'bg-accent-soft shadow-[0_0_0_2px_var(--best)]',
          finalized && 'bg-yes-soft shadow-[0_0_0_2px_var(--yes)]',
          hovered && !best && !finalized && 'bg-secondary',
        )}
      >
        {label.secondary ? (
          <>
            <span className="text-[0.6875rem] tracking-wide text-muted-foreground uppercase">
              {label.primary}
            </span>
            <span className="text-sm font-semibold whitespace-nowrap">{label.secondary}</span>
          </>
        ) : (
          <span className="text-sm font-semibold text-balance">{label.primary}</span>
        )}
        {label.tertiary && (
          <span className="text-[0.6875rem] whitespace-nowrap text-muted-foreground">
            {label.tertiary}
          </span>
        )}
      </div>
    </th>
  )
}
