import { motion } from 'motion/react'
import { Check } from 'lucide-react'
import { spring, useReducedMotion } from '#/lib/motion'
import { cn } from '#/lib/utils'

/**
 * The three wizard steps as pills. Completed steps stay clickable so an organiser can jump back
 * to fix a title without losing their place; steps ahead are disabled until they're reachable.
 */
export function StepIndicator({
  step,
  labels,
  onSelect,
}: {
  step: number
  labels: string[]
  onSelect: (step: number) => void
}) {
  const reduceMotion = useReducedMotion()

  return (
    <ol className="flex items-center gap-1 sm:gap-2">
      {labels.map((label, index) => {
        const active = index === step
        const done = index < step

        return (
          <li key={label} className="min-w-0">
            <button
              type="button"
              disabled={index > step}
              aria-current={active ? 'step' : undefined}
              onClick={() => onSelect(index)}
              className={cn(
                'focus-ring relative flex items-center gap-2 rounded-full px-2.5 py-1.5 text-sm font-medium transition-colors sm:px-3.5',
                active ? 'text-foreground' : 'text-muted-foreground',
                done && 'hover:text-foreground',
                index > step && 'opacity-60',
              )}
            >
              <span
                className={cn(
                  'flex size-5 shrink-0 items-center justify-center rounded-full text-[0.7rem] font-semibold transition-colors',
                  active && 'bg-primary-strong text-primary-foreground',
                  done && 'bg-yes-soft text-yes-ink',
                  !active && !done && 'bg-muted text-muted-foreground',
                )}
              >
                {done ? <Check aria-hidden="true" className="size-3" /> : index + 1}
              </span>
              {/* On a narrow screen only the step you're on spells itself out; the others stay
                  numbered dots so all three fit without wrapping. */}
              <span className={cn('truncate', !active && 'max-sm:sr-only')}>{label}</span>

              {active && (
                <motion.span
                  layoutId={reduceMotion ? undefined : 'creator-step-underline'}
                  transition={spring}
                  aria-hidden="true"
                  className="absolute inset-x-2 -bottom-px h-0.5 rounded-full bg-primary"
                />
              )}
            </button>
          </li>
        )
      })}
    </ol>
  )
}
