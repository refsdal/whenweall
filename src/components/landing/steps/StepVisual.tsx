import type { CSSProperties, ReactNode } from 'react'
import { cn } from '#/lib/utils'

/**
 * How far into the shared 12-second cycle each card begins. Offsetting rather than giving each its
 * own loop means exactly one illustration is moving at a time, and the row plays left to right as
 * the sequence it describes.
 */
export const STEP_CYCLE_OFFSETS_S = [0, 4, 8] as const

/** Negative delays start a loop mid-cycle, which is what keeps the three phase-locked. */
export function stepDelay(offsetS: number, extraS = 0): CSSProperties {
  return { animationDelay: `${-offsetS + extraS}s` }
}

/**
 * Shared frame for the three "how it works" illustrations. Decorative by definition, so the whole
 * thing is one labelled image to assistive tech and its innards are hidden — the step's own
 * heading and body already carry the meaning.
 */
export function StepVisual({
  label,
  className,
  children,
}: {
  label: string
  className?: string
  children: ReactNode
}) {
  return (
    <div
      role="img"
      aria-label={label}
      className={cn(
        'flex h-24 w-full items-center justify-center overflow-hidden rounded-xl bg-secondary/50 px-3',
        className,
      )}
    >
      <div aria-hidden="true" className="w-full">
        {children}
      </div>
    </div>
  )
}
