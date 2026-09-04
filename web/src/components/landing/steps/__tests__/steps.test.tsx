import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { DecideTogether } from '#/components/landing/steps/DecideTogether'
import { ProposeTimes } from '#/components/landing/steps/ProposeTimes'
import { ShareLink } from '#/components/landing/steps/ShareLink'
import { stepDelay, STEP_CYCLE_OFFSETS_S } from '#/components/landing/steps/StepVisual'

afterEach(cleanup)

/*
 * CSS keyframe timing is not meaningfully testable in jsdom, and pretending otherwise would be
 * worse than not testing it. What these cover is the part that can silently break: the
 * accessibility contract, and the negative-delay arithmetic that keeps the three cards
 * phase-locked to one shared cycle.
 */

describe('step cycle offsets', () => {
  it('spaces the three cards evenly across the shared cycle', () => {
    expect(STEP_CYCLE_OFFSETS_S).toEqual([0, 4, 8])
  })

  it('uses negative delays so a card starts mid-cycle rather than waiting', () => {
    // A positive delay would leave the later cards blank until their turn came round.
    expect(stepDelay(4)).toEqual({ animationDelay: '-4s' })
    expect(stepDelay(8, 0.5)).toEqual({ animationDelay: '-7.5s' })
    expect(stepDelay(0)).toEqual({ animationDelay: '0s' })
  })
})

describe('step illustrations', () => {
  it('each exposes one labelled image and hides its decorative innards', () => {
    for (const Step of [ProposeTimes, ShareLink, DecideTogether]) {
      const { unmount } = render(<Step />)
      const image = screen.getByRole('img')

      expect(image).toHaveAccessibleName()
      expect(image.getAttribute('aria-label')!.length).toBeGreaterThan(10)
      expect(image.querySelector('[aria-hidden="true"]')).not.toBeNull()

      unmount()
    }
  })

  it('staggers the proposed dates so they land one after another', () => {
    const { container } = render(<ProposeTimes />)
    const delays = [...container.querySelectorAll('[data-step-animated]')].map(
      (el) => (el as HTMLElement).style.animationDelay,
    )

    expect(delays).toHaveLength(3)
    expect(new Set(delays).size).toBe(3)
  })

  it('gives every vote cell a resolved colour to hold under reduced motion', () => {
    const { container } = render(<DecideTogether />)
    const cells = [...container.querySelectorAll('[data-step-cell]')]

    expect(cells).toHaveLength(9)
    // The reduced-motion rule falls back to `--step-cell-bg`, so a cell without one would freeze
    // blank rather than on its answer colour.
    for (const cell of cells) {
      expect((cell as HTMLElement).style.getPropertyValue('--step-cell-bg')).not.toBe('')
    }
  })
})
