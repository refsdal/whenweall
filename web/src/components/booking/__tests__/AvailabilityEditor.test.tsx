import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AvailabilityEditor } from '#/components/booking/AvailabilityEditor'
import { initialDraft, type DayIssue, type DraftRange } from '#/components/booking/editor-state'

afterEach(() => cleanup())

function renderEditor(
  options: {
    availability?: Record<string, DraftRange[]>
    issues?: Record<string, DayIssue[]>
  } = {},
) {
  const dispatch = vi.fn()
  render(
    <AvailabilityEditor
      availability={options.availability ?? initialDraft('Europe/Oslo').availability}
      issues={options.issues ?? {}}
      timezone="Europe/Oslo"
      dispatch={dispatch}
    />,
  )
  return { dispatch }
}

describe('AvailabilityEditor', () => {
  it('renders one row per weekday, Monday first', () => {
    renderEditor()

    const toggles = screen.getAllByRole('switch')
    expect(toggles).toHaveLength(7)
    expect(toggles[0]).toHaveAccessibleName(/monday/i)
    expect(toggles[6]).toHaveAccessibleName(/sunday/i)
  })

  it('marks the weekend as unavailable by default', () => {
    renderEditor()

    expect(screen.getByRole('switch', { name: /saturday/i })).not.toBeChecked()
    expect(screen.getByRole('switch', { name: /monday/i })).toBeChecked()
  })

  it('clears a day’s hours when it is toggled off', async () => {
    const user = userEvent.setup()
    const { dispatch } = renderEditor()

    await user.click(screen.getByRole('switch', { name: /monday/i }))

    expect(dispatch).toHaveBeenCalledWith({ type: 'setDayRanges', weekday: '1', ranges: [] })
  })

  it('adds a first window when an unavailable day is toggled on', async () => {
    const user = userEvent.setup()
    const { dispatch } = renderEditor()

    await user.click(screen.getByRole('switch', { name: /saturday/i }))

    expect(dispatch).toHaveBeenCalledWith({ type: 'addRange', weekday: '6' })
  })

  it('adds another window from the day’s add button', async () => {
    const user = userEvent.setup()
    const { dispatch } = renderEditor()

    await user.click(screen.getByRole('button', { name: /add hours on monday/i }))

    expect(dispatch).toHaveBeenCalledWith({ type: 'addRange', weekday: '1' })
  })

  it('reports an edited start time as the day’s new ranges', () => {
    const { dispatch } = renderEditor()

    fireEvent.change(screen.getByLabelText(/monday start time/i), { target: { value: '10:00' } })

    expect(dispatch).toHaveBeenCalledWith({
      type: 'setDayRanges',
      weekday: '1',
      ranges: [{ start: '10:00', end: '17:00' }],
    })
  })

  it('reports an edited end time as the day’s new ranges', () => {
    const { dispatch } = renderEditor()

    fireEvent.change(screen.getByLabelText(/monday end time/i), { target: { value: '16:30' } })

    expect(dispatch).toHaveBeenCalledWith({
      type: 'setDayRanges',
      weekday: '1',
      ranges: [{ start: '09:00', end: '16:30' }],
    })
  })

  it('removes one window by index', async () => {
    const user = userEvent.setup()
    const availability = {
      ...initialDraft('UTC').availability,
      '1': [
        { start: '09:00', end: '12:00' },
        { start: '13:00', end: '17:00' },
      ],
    }
    const { dispatch } = renderEditor({ availability })

    await user.click(screen.getByRole('button', { name: /remove 13:00–17:00 on monday/i }))

    expect(dispatch).toHaveBeenCalledWith({ type: 'removeRange', weekday: '1', index: 1 })
  })

  it('copies a day to every day', async () => {
    const user = userEvent.setup()
    const { dispatch } = renderEditor()

    await user.click(screen.getByRole('button', { name: /copy to all days from monday/i }))

    expect(dispatch).toHaveBeenCalledWith({ type: 'copyDayToAll', weekday: '1' })
  })

  it('offers no copy-to-all on a day that is off', () => {
    renderEditor()

    expect(
      screen.queryByRole('button', { name: /copy to all days from saturday/i }),
    ).not.toBeInTheDocument()
  })

  it('shows an overlap message on the day that has one', () => {
    const availability = {
      ...initialDraft('UTC').availability,
      '1': [
        { start: '09:00', end: '14:00' },
        { start: '13:00', end: '17:00' },
      ],
    }
    renderEditor({ availability, issues: { '1': [{ index: 1, code: 'overlap' }] } })

    expect(screen.getByText(/overlap/i)).toBeInTheDocument()
  })

  it('shows an ordering message when a window ends before it starts', () => {
    const availability = {
      ...initialDraft('UTC').availability,
      '1': [{ start: '17:00', end: '09:00' }],
    }
    renderEditor({ availability, issues: { '1': [{ index: 0, code: 'order' }] } })

    expect(screen.getByText(/after the start time/i)).toBeInTheDocument()
  })

  it('shows an alignment message for a time off the 15-minute grid', () => {
    const availability = {
      ...initialDraft('UTC').availability,
      '1': [{ start: '09:07', end: '17:00' }],
    }
    renderEditor({ availability, issues: { '1': [{ index: 0, code: 'unaligned' }] } })

    expect(screen.getByText(/15-minute grid/i)).toBeInTheDocument()
  })

  it('warns when every day is off', () => {
    const availability = Object.fromEntries(
      ['0', '1', '2', '3', '4', '5', '6'].map((d) => [d, [] as DraftRange[]]),
    )
    renderEditor({ availability })

    expect(screen.getByText(/add hours to at least one day/i)).toBeInTheDocument()
  })
})
