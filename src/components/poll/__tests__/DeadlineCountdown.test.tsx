import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { DeadlineCountdown, formatCountdown } from '#/components/poll/DeadlineCountdown'

afterEach(() => cleanup())

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

describe('formatCountdown', () => {
  it('shows days and hours when more than a day is left', () => {
    expect(formatCountdown(2 * DAY + 4 * HOUR + 30 * MINUTE, 'en')).toBe('2d 4h')
  })

  it('shows hours and minutes within the last day', () => {
    expect(formatCountdown(4 * HOUR + 5 * MINUTE, 'en')).toBe('4h 5m')
  })

  it('shows minutes within the last hour', () => {
    expect(formatCountdown(35 * MINUTE, 'en')).toBe('35m')
  })

  it('collapses the last minute into a phrase', () => {
    expect(formatCountdown(20_000, 'en')).toBe('under a minute')
  })

  it('reports a deadline in the past as closed', () => {
    expect(formatCountdown(0, 'en')).toBe('Closed')
    expect(formatCountdown(-5 * DAY, 'en')).toBe('Closed')
  })

  it('translates the units', () => {
    expect(formatCountdown(2 * DAY + 4 * HOUR, 'nb')).toBe('2d 4t')
  })
})

describe('DeadlineCountdown', () => {
  it('renders the remaining time for a future deadline', () => {
    const deadline = new Date(Date.now() + 2 * DAY + 4 * HOUR + MINUTE).toISOString()
    render(<DeadlineCountdown deadlineAt={deadline} />)

    expect(screen.getByText(/2d 4h/)).toBeInTheDocument()
  })

  it('renders "Closed" for a deadline in the past', () => {
    const deadline = new Date(Date.now() - HOUR).toISOString()
    render(<DeadlineCountdown deadlineAt={deadline} />)

    expect(screen.getByText(/closed/i)).toBeInTheDocument()
  })
})
