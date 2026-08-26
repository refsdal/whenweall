import type { ComponentProps } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TimeSlotEditor } from '#/components/creator/TimeSlotEditor'

afterEach(() => cleanup())

function renderEditor(props: Partial<ComponentProps<typeof TimeSlotEditor>> = {}) {
  const onAdd = vi.fn()
  const onRemove = vi.fn()
  const onApplyToAll = vi.fn()
  render(
    <TimeSlotEditor
      date="2026-06-15"
      slots={[]}
      onAdd={onAdd}
      onRemove={onRemove}
      onApplyToAll={onApplyToAll}
      {...props}
    />,
  )
  return { onAdd, onRemove, onApplyToAll }
}

function setTime(name: RegExp, value: string) {
  fireEvent.change(screen.getByLabelText(name), { target: { value } })
}

function durationChip(name: string) {
  return screen.getByRole('button', { name })
}

describe('TimeSlotEditor', () => {
  it('adds a slot, deriving the end time from the start and the selected duration chip', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '18:00')
    await user.click(durationChip('2 h'))
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('18:00', '20:00')
  })

  it('wraps a duration past midnight (23:30 + 1h -> 00:30) and renders the next-day marker', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '23:30')
    // "1 h" is the default duration already, but select it explicitly for clarity.
    await user.click(durationChip('1 h'))
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('23:30', '00:30')

    cleanup()
    renderEditor({ slots: [{ start: '23:30', end: '00:30' }] })
    expect(screen.getByTitle(/ends the next day/i)).toBeInTheDocument()
  })

  it('adds an open-ended slot when "No end" is selected', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '18:00')
    await user.click(durationChip('No end'))
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('18:00', null)
  })

  it('keeps the chosen duration selected across consecutive adds, clearing only the start', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '09:00')
    await user.click(durationChip('30 min'))
    await user.click(screen.getByRole('button', { name: /add time/i }))
    expect(onAdd).toHaveBeenNthCalledWith(1, '09:00', '09:30')
    expect(screen.getByLabelText(/start/i)).toHaveValue('')
    expect(durationChip('30 min')).toHaveAttribute('aria-pressed', 'true')

    setTime(/start/i, '13:00')
    await user.click(screen.getByRole('button', { name: /add time/i }))
    expect(onAdd).toHaveBeenNthCalledWith(2, '13:00', '13:30')
  })

  it('defaults to a 1 h duration on first use', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '09:00')
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('09:00', '10:00')
    expect(durationChip('1 h')).toHaveAttribute('aria-pressed', 'true')
  })

  it('a quick-start chip sets the start field', async () => {
    const user = userEvent.setup()
    renderEditor()

    await user.click(screen.getByRole('button', { name: '18:00' }))

    expect(screen.getByLabelText(/start/i)).toHaveValue('18:00')
  })

  it('selecting "Custom" reveals the end time field', async () => {
    const user = userEvent.setup()
    renderEditor()

    expect(screen.queryByLabelText(/end/i)).not.toBeInTheDocument()

    await user.click(durationChip('Custom'))

    expect(screen.getByLabelText(/end/i)).toBeInTheDocument()
  })

  it('shows a live "start – end" preview once a start time is chosen', async () => {
    const user = userEvent.setup()
    renderEditor()

    setTime(/start/i, '18:00')
    await user.click(durationChip('2 h'))

    expect(screen.getByText('18:00 – 20:00')).toBeInTheDocument()
  })

  it('the preview shows only the start time for an open-ended slot', async () => {
    const user = userEvent.setup()
    renderEditor()

    setTime(/start/i, '18:00')
    await user.click(durationChip('No end'))

    expect(screen.getByText('18:00', { selector: 'span' })).toBeInTheDocument()
  })

  it('adds a slot with a custom end time', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '09:00')
    await user.click(durationChip('Custom'))
    setTime(/end/i, '10:30')
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('09:00', '10:30')
  })

  it('adds a slot when Enter is pressed in the start field, using the current duration', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '09:00')
    await user.click(durationChip('No end'))
    await user.type(screen.getByLabelText(/start/i), '{Enter}')

    expect(onAdd).toHaveBeenCalledWith('09:00', null)
  })

  it('clears the start field after a slot is added', async () => {
    const user = userEvent.setup()
    renderEditor()

    setTime(/start/i, '09:00')
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(screen.getByLabelText(/start/i)).toHaveValue('')
  })

  it('clears the custom end field after a slot is added, while staying in custom mode', async () => {
    const user = userEvent.setup()
    renderEditor()

    setTime(/start/i, '09:00')
    await user.click(durationChip('Custom'))
    setTime(/end/i, '10:00')
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(screen.getByLabelText(/start/i)).toHaveValue('')
    expect(screen.getByLabelText(/end/i)).toHaveValue('')
  })

  it('cannot add a slot without a start time', () => {
    renderEditor()

    expect(screen.getByRole('button', { name: /add time/i })).toBeDisabled()
  })

  it('constrains the end time field to no earlier than the chosen start time', async () => {
    const user = userEvent.setup()
    renderEditor()

    await user.click(durationChip('Custom'))
    setTime(/start/i, '09:00')

    expect(screen.getByLabelText(/end/i)).toHaveAttribute('min', '09:00')
  })

  it('bumps an earlier end time up to the newly chosen start time', async () => {
    const user = userEvent.setup()
    renderEditor()

    await user.click(durationChip('Custom'))
    setTime(/start/i, '09:00')
    setTime(/end/i, '08:00')
    setTime(/start/i, '11:00')

    expect(screen.getByLabelText(/end/i)).toHaveValue('11:00')
  })

  it('leaves a still-valid end time untouched when the start time changes', async () => {
    const user = userEvent.setup()
    renderEditor()

    await user.click(durationChip('Custom'))
    setTime(/start/i, '09:00')
    setTime(/end/i, '12:00')
    setTime(/start/i, '10:00')

    expect(screen.getByLabelText(/end/i)).toHaveValue('12:00')
  })

  it('lists the slots it is given and removes one by index', async () => {
    const user = userEvent.setup()
    const { onRemove } = renderEditor({
      slots: [
        { start: '09:00', end: '10:00' },
        { start: '13:00', end: null },
      ],
    })

    expect(screen.getByText(/09:00/)).toBeInTheDocument()
    expect(screen.getByText(/13:00/)).toBeInTheDocument()

    await user.click(screen.getAllByRole('button', { name: /remove/i })[1] as HTMLElement)

    expect(onRemove).toHaveBeenCalledWith(1)
  })

  it('hides apply-to-all while the day has no slots', () => {
    renderEditor({ slots: [] })

    expect(screen.queryByRole('button', { name: /apply to all/i })).not.toBeInTheDocument()
  })

  it('offers apply-to-all once the day has slots', () => {
    renderEditor({ slots: [{ start: '09:00', end: null }] })

    expect(screen.getByRole('button', { name: /apply to all/i })).toBeInTheDocument()
  })

  it('calls onApplyToAll when the button is pressed', async () => {
    const user = userEvent.setup()
    const { onApplyToAll } = renderEditor({ slots: [{ start: '09:00', end: null }] })

    await user.click(screen.getByRole('button', { name: /apply to all/i }))

    expect(onApplyToAll).toHaveBeenCalled()
  })

  it('shows the all-day hint only when the day has zero slots', () => {
    renderEditor({ slots: [] })
    expect(screen.getByText(/all day/i)).toBeInTheDocument()
    cleanup()

    renderEditor({ slots: [{ start: '09:00', end: null }] })
    expect(screen.queryByText(/all day.*narrow/i)).not.toBeInTheDocument()
  })
})

describe('TimeSlotEditor / showCapacity', () => {
  it('does not show a capacity field or pass a capacity to onAdd by default', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    expect(screen.queryAllByRole('spinbutton')).toHaveLength(0)

    setTime(/start/i, '09:00')
    await user.click(durationChip('No end'))
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('09:00', null)
  })

  it('includes a capacity (defaulting to 1) in onAdd when showCapacity is set', async () => {
    const user = userEvent.setup()
    const onAdd = vi.fn()
    render(
      <TimeSlotEditor date="2026-06-15" slots={[]} onAdd={onAdd} onRemove={vi.fn()} showCapacity />,
    )

    setTime(/start/i, '09:00')
    await user.click(durationChip('No end'))
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('09:00', null, 1)
  })

  it('shows a capacity field per existing slot and reports changes by index', () => {
    const onSetCapacity = vi.fn()
    render(
      <TimeSlotEditor
        date="2026-06-15"
        slots={[
          { start: '09:00', end: null, capacity: 2 },
          { start: '13:00', end: null },
        ]}
        onAdd={vi.fn()}
        onRemove={vi.fn()}
        onSetCapacity={onSetCapacity}
        showCapacity
      />,
    )

    const spinbuttons = screen.getAllByRole('spinbutton')
    // One per existing slot, plus the add-form's own capacity field.
    expect(spinbuttons).toHaveLength(3)
    expect(spinbuttons[0]).toHaveValue(2)
    expect(spinbuttons[1]).toHaveValue(1)

    fireEvent.change(spinbuttons[1] as HTMLElement, { target: { value: '5' } })

    expect(onSetCapacity).toHaveBeenCalledWith(1, 5)
  })
})
