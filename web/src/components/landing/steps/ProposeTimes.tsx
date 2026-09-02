import { intlLocale, getLocale, m } from '#/lib/i18n'
import { StepVisual, stepDelay, STEP_CYCLE_OFFSETS_S } from './StepVisual'

// Fixed dates so server and client agree; formatted per locale like `VoteGridMock` does.
const DATES = [new Date(2027, 4, 11), new Date(2027, 4, 12), new Date(2027, 4, 13)]

/** Option chips dropping into the list, one after another — what "propose times" actually is. */
export function ProposeTimes() {
  const fmt = new Intl.DateTimeFormat(intlLocale(getLocale()), {
    weekday: 'short',
    day: 'numeric',
    month: 'short',
  })

  return (
    <StepVisual label={m.landing_step_1_visual_alt()}>
      <ul className="flex flex-col gap-1.5">
        {DATES.map((date, index) => (
          <li
            key={date.toISOString()}
            data-step-animated
            // Each chip lands a beat after the one above it, inside this card's own window.
            style={stepDelay(STEP_CYCLE_OFFSETS_S[0], index * 0.34)}
            className="animate-step-drop flex items-center justify-between rounded-lg bg-card px-2.5 py-1.5 text-[0.6875rem] shadow-sm"
          >
            <span className="font-medium">{fmt.format(date)}</span>
            <span className="size-1.5 rounded-full bg-[var(--primary)]" />
          </li>
        ))}
      </ul>
    </StepVisual>
  )
}
