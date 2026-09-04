import type { CSSProperties } from 'react'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { StepVisual, stepDelay, STEP_CYCLE_OFFSETS_S } from './StepVisual'

const OFFSET = STEP_CYCLE_OFFSETS_S[2]
const WINNING_COLUMN = 1

type Answer = 'yes' | 'ifneedbe' | 'no'

const ANSWER_BG: Record<Answer, string> = {
  yes: 'var(--yes-soft)',
  ifneedbe: 'var(--ifneedbe-soft)',
  no: 'var(--no-soft)',
}

// Column 1 is unanimous, which is what makes it the winner.
const ROWS: Answer[][] = [
  ['yes', 'yes', 'ifneedbe'],
  ['ifneedbe', 'yes', 'no'],
  ['no', 'yes', 'yes'],
]

/** Votes landing cell by cell, then the winning column crowned. */
export function DecideTogether() {
  return (
    <StepVisual label={m.landing_step_3_visual_alt()}>
      <div className="flex flex-col items-center gap-1.5">
        <div className="flex gap-1.5">
          {ROWS[0]!.map((_, column) => (
            <span
              key={column}
              data-step-animated
              style={stepDelay(OFFSET)}
              className={cn(
                'animate-step-crown h-1.5 w-8 rounded-full',
                column === WINNING_COLUMN ? 'bg-[var(--primary)]' : 'bg-transparent',
              )}
            />
          ))}
        </div>

        {ROWS.map((row, rowIndex) => (
          <div key={rowIndex} className="flex gap-1.5">
            {row.map((answer, column) => (
              <span
                key={column}
                data-step-cell
                style={
                  {
                    '--step-cell-bg': ANSWER_BG[answer],
                    ...stepDelay(OFFSET, (rowIndex * row.length + column) * 0.09),
                  } as CSSProperties
                }
                className="animate-step-fill h-5 w-8 rounded-md"
              />
            ))}
          </div>
        ))}
      </div>
    </StepVisual>
  )
}
