import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { splitAroundSlot, UsageStatsSection } from '#/components/landing/UsageStats'
import type { UsageStats } from '#/do/stats-protocol'

const STATS: UsageStats = {
  pollsFinalized: 340,
  pollsCreated: 1204,
  responsesYes: 2610,
  responsesIfNeedBe: 890,
  responsesNo: 400,
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('UsageStatsSection', () => {
  it('renders the decided count inside the sentence, highlighted', () => {
    render(<UsageStatsSection initial={STATS} />)

    const number = screen.getByText('340')
    expect(number).toBeInTheDocument()
    // The highlight is what makes the number readable as the subject of the sentence.
    expect(number.className).toContain('font-semibold')
  })

  it('keeps the sentence intact around the number', () => {
    const { container } = render(<UsageStatsSection initial={STATS} />)
    const sentence = container.querySelector('p.display')!

    // The sentinel must never survive into the DOM, and the prose either side of the number must
    // still be there — a split on the wrong character would shred the sentence into words.
    expect(sentence.textContent).not.toContain('\u0000')
    expect(sentence.textContent).toContain('340')
    expect(sentence.textContent!.replace('340', '').trim().length).toBeGreaterThan(10)
  })

  it('renders the supporting figures and the answer breakdown', () => {
    render(<UsageStatsSection initial={STATS} />)

    expect(screen.getByText(/1,204/)).toBeInTheDocument()
    // 2610 + 890 + 400
    expect(screen.getByText(/3,900/)).toBeInTheDocument()
    expect(screen.getByText('2,610')).toBeInTheDocument()
    expect(screen.getByText('890')).toBeInTheDocument()
    expect(screen.getByText('400')).toBeInTheDocument()
  })

  it('shows a zero decided count rather than hiding the section', () => {
    // The review decided low numbers are the message, not something to hide behind a threshold.
    render(<UsageStatsSection initial={{ ...STATS, pollsFinalized: 0 }} />)
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('splits around the marker wherever a locale places it', () => {
    // Proves the split is not secretly assuming the number comes last.
    expect(splitAroundSlot('settled \u0000 dates')).toEqual(['settled ', ' dates'])
    expect(splitAroundSlot('\u0000 dates settled')).toEqual(['', ' dates settled'])
    expect(splitAroundSlot('dates settled: \u0000')).toEqual(['dates settled: ', ''])
  })

  it('renders plain prose when a translation lost its placeholder', () => {
    expect(splitAroundSlot('no placeholder here')).toEqual(['no placeholder here'])
  })
})
