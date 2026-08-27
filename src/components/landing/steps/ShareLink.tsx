import { Check, Link2 } from 'lucide-react'
import { m } from '#/lib/i18n'
import { StepVisual, stepDelay, STEP_CYCLE_OFFSETS_S } from './StepVisual'

const OFFSET = STEP_CYCLE_OFFSETS_S[1]

// Same four people as the hero illustration, so the landing page tells one consistent story.
const INITIALS = ['K', 'A', 'R', 'L']

/** A link being copied, then people arriving through it. */
export function ShareLink() {
  return (
    <StepVisual label={m.landing_step_2_visual_alt()}>
      <div className="flex flex-col gap-2.5">
        <div className="flex items-center gap-2 rounded-lg bg-card px-2.5 py-1.5 shadow-sm">
          <Link2 className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="flex-1 truncate text-[0.6875rem] text-muted-foreground">
            whenweall.com/p/k3f9
          </span>
          <span
            data-step-animated
            style={stepDelay(OFFSET)}
            className="animate-step-copy inline-flex size-4 shrink-0 items-center justify-center rounded-full bg-[var(--yes-soft)] text-[var(--yes-ink)]"
          >
            <Check className="size-2.5" />
          </span>
        </div>

        <div className="flex items-center gap-1.5 pl-0.5">
          {INITIALS.map((initial, index) => (
            <span
              key={initial}
              data-step-animated
              style={stepDelay(OFFSET, index * 0.22)}
              className="animate-step-arrive inline-flex size-6 items-center justify-center rounded-full bg-card text-[0.625rem] font-semibold shadow-sm"
            >
              {initial}
            </span>
          ))}
        </div>
      </div>
    </StepVisual>
  )
}
