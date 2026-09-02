import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MonthPicker } from '#/components/booking/MonthPicker'

afterEach(() => cleanup())

function renderPicker(overrides: Partial<React.ComponentProps<typeof MonthPicker>> = {}) {
  const onSelect = vi.fn()
  const onMonthChange = vi.fn()
  render(
    <MonthPicker
      month="2026-09"
      availableDays={['2026-09-15', '2026-09-16']}
      selected={null}
      onSelect={onSelect}
      onMonthChange={onMonthChange}
      {...overrides}
    />,
  )
  return { onSelect, onMonthChange }
}

function dayButton(day: string): HTMLButtonElement {
  const cell = document.querySelector(`[data-day="${day}"]`)
  const button = cell?.querySelector('button')
  if (!button) throw new Error(`no day button for ${day}`)
  return button
}

describe('MonthPicker', () => {
  it('enables days that have at least one slot', () => {
    renderPicker()

    expect(dayButton('2026-09-15')).toBeEnabled()
    expect(dayButton('2026-09-16')).toBeEnabled()
  })

  it('disables days with no slots', () => {
    renderPicker()

    expect(dayButton('2026-09-17')).toBeDisabled()
    expect(dayButton('2026-09-01')).toBeDisabled()
  })

  it('calls onSelect with the picked day key', async () => {
    const user = userEvent.setup()
    const { onSelect } = renderPicker()

    await user.click(dayButton('2026-09-16'))

    expect(onSelect).toHaveBeenCalledWith('2026-09-16')
  })

  it('does not call onSelect for a day without slots', async () => {
    const user = userEvent.setup()
    const { onSelect } = renderPicker()

    await user.click(dayButton('2026-09-17'))

    expect(onSelect).not.toHaveBeenCalled()
  })

  it('reports the new month when the visitor navigates', async () => {
    const user = userEvent.setup()
    const { onMonthChange } = renderPicker()

    await user.click(screen.getByRole('button', { name: /next month/i }))

    expect(onMonthChange).toHaveBeenCalledWith('2026-10')
  })

  it('marks the selected day', () => {
    renderPicker({ selected: '2026-09-15' })

    expect(document.querySelector('[data-day="2026-09-15"]')).toHaveAttribute(
      'data-selected',
      'true',
    )
  })
})
