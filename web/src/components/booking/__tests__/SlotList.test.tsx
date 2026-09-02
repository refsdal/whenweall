import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SlotList } from '#/components/booking/SlotList'

afterEach(() => cleanup())

const SLOTS = [
  { start: '2026-09-15T07:00:00.000Z', end: '2026-09-15T07:30:00.000Z' },
  { start: '2026-09-15T07:30:00.000Z', end: '2026-09-15T08:00:00.000Z' },
]

describe('SlotList', () => {
  it('renders one chip per slot, in the given timezone', () => {
    render(<SlotList day="2026-09-15" slots={SLOTS} timeZone="Europe/Oslo" onPick={vi.fn()} />)

    const chips = screen.getAllByRole('button')
    expect(chips).toHaveLength(2)
    expect(chips[0]).toHaveTextContent('09:00')
    expect(chips[1]).toHaveTextContent('09:30')
  })

  it('re-reads the same slots in another timezone', () => {
    render(<SlotList day="2026-09-15" slots={SLOTS} timeZone="UTC" onPick={vi.fn()} />)

    expect(screen.getAllByRole('button')[0]).toHaveTextContent('07:00')
  })

  it('gives each chip an accessible name with the full time range', () => {
    render(<SlotList day="2026-09-15" slots={SLOTS} timeZone="Europe/Oslo" onPick={vi.fn()} />)

    expect(screen.getAllByRole('button')[0]).toHaveAccessibleName(/09:00.*09:30/)
  })

  it('calls onPick with the chosen slot', async () => {
    const user = userEvent.setup()
    const onPick = vi.fn()
    render(<SlotList day="2026-09-15" slots={SLOTS} timeZone="Europe/Oslo" onPick={onPick} />)

    await user.click(screen.getAllByRole('button')[1] as HTMLElement)

    expect(onPick).toHaveBeenCalledWith(SLOTS[1])
  })

  it('shows an empty state when the day has no slots left', () => {
    render(<SlotList day="2026-09-15" slots={[]} timeZone="Europe/Oslo" onPick={vi.fn()} />)

    expect(screen.queryAllByRole('button')).toHaveLength(0)
    expect(screen.getByTestId('slot-list-empty')).toBeInTheDocument()
  })
})
