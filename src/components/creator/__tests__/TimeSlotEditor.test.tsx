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

describe('TimeSlotEditor', () => {
  it('adds a slot with a start and an end time', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '09:00')
    setTime(/end/i, '10:30')
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('09:00', '10:30')
  })

  it('adds a slot with no end time', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '18:00')
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(onAdd).toHaveBeenCalledWith('18:00', null)
  })

  it('adds a slot when Enter is pressed in the start field', async () => {
    const user = userEvent.setup()
    const { onAdd } = renderEditor()

    setTime(/start/i, '09:00')
    await user.type(screen.getByLabelText(/start/i), '{Enter}')

    expect(onAdd).toHaveBeenCalledWith('09:00', null)
  })

  it('clears the fields after a slot is added', async () => {
    const user = userEvent.setup()
    renderEditor()

    setTime(/start/i, '09:00')
    setTime(/end/i, '10:00')
    await user.click(screen.getByRole('button', { name: /add time/i }))

    expect(screen.getByLabelText(/start/i)).toHaveValue('')
    expect(screen.getByLabelText(/end/i)).toHaveValue('')
  })

  it('cannot add a slot without a start time', () => {
    renderEditor()

    expect(screen.getByRole('button', { name: /add time/i })).toBeDisabled()
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
})
