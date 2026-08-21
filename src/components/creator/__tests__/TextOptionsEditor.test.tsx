import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TextOptionsEditor } from '#/components/creator/TextOptionsEditor'

afterEach(() => cleanup())

function rows(): HTMLInputElement[] {
  return screen.getAllByRole('textbox') as HTMLInputElement[]
}

describe('TextOptionsEditor', () => {
  it('renders one input per option, plus an empty row to type into when there are none', () => {
    render(<TextOptionsEditor value={[]} onChange={vi.fn()} />)

    expect(rows().length).toBeGreaterThanOrEqual(1)
    expect(rows()[0]).toHaveValue('')
  })

  it('renders the options it is given', () => {
    render(
      <TextOptionsEditor value={[{ label: 'Pizza' }, { label: 'Sushi' }]} onChange={vi.fn()} />,
    )

    expect(rows()[0]).toHaveValue('Pizza')
    expect(rows()[1]).toHaveValue('Sushi')
  })

  it('reports typed options, dropping the blank rows', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<TextOptionsEditor value={[{ label: '' }, { label: '' }]} onChange={onChange} />)

    await user.type(rows()[0] as HTMLInputElement, 'Pizza')

    expect(onChange).toHaveBeenLastCalledWith([{ label: 'Pizza' }])
  })

  it('trims what it reports', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<TextOptionsEditor value={[{ label: '' }]} onChange={onChange} />)

    await user.type(rows()[0] as HTMLInputElement, '  Pizza  ')

    expect(onChange).toHaveBeenLastCalledWith([{ label: 'Pizza' }])
  })

  it('keeps the id of an option when its label is edited', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<TextOptionsEditor value={[{ id: 'opt-1', label: 'Pizza' }]} onChange={onChange} />)

    await user.type(rows()[0] as HTMLInputElement, '!')

    expect(onChange).toHaveBeenLastCalledWith([{ id: 'opt-1', label: 'Pizza!' }])
  })

  it('adds a new row below on Enter and moves focus into it', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<TextOptionsEditor value={[{ label: 'Pizza' }]} onChange={onChange} />)

    await user.click(rows()[0] as HTMLInputElement)
    await user.keyboard('{Enter}')

    expect(rows()).toHaveLength(2)
    expect(rows()[1]).toHaveFocus()

    await user.keyboard('Sushi')
    expect(onChange).toHaveBeenLastCalledWith([{ label: 'Pizza' }, { label: 'Sushi' }])
  })

  it('adds a row after the focused one, not at the end', async () => {
    const user = userEvent.setup()
    render(
      <TextOptionsEditor value={[{ label: 'Pizza' }, { label: 'Sushi' }]} onChange={vi.fn()} />,
    )

    await user.click(rows()[0] as HTMLInputElement)
    await user.keyboard('{Enter}')

    expect(rows()).toHaveLength(3)
    expect(rows()[1]).toHaveValue('')
    expect(rows()[2]).toHaveValue('Sushi')
  })

  it('removes an empty row on Backspace and puts focus on the row above', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<TextOptionsEditor value={[{ label: 'Pizza' }, { label: '' }]} onChange={onChange} />)

    await user.click(rows()[1] as HTMLInputElement)
    await user.keyboard('{Backspace}')

    expect(rows()).toHaveLength(1)
    expect(rows()[0]).toHaveFocus()
    expect(onChange).toHaveBeenLastCalledWith([{ label: 'Pizza' }])
  })

  it('keeps the last row when Backspace is pressed in it', async () => {
    const user = userEvent.setup()
    render(<TextOptionsEditor value={[{ label: '' }]} onChange={vi.fn()} />)

    await user.click(rows()[0] as HTMLInputElement)
    await user.keyboard('{Backspace}')

    expect(rows()).toHaveLength(1)
  })

  it('does not remove a row that still has text', async () => {
    const user = userEvent.setup()
    render(
      <TextOptionsEditor value={[{ label: 'Pizza' }, { label: 'Sushi' }]} onChange={vi.fn()} />,
    )

    await user.click(rows()[1] as HTMLInputElement)
    await user.keyboard('{Backspace}')

    expect(rows()).toHaveLength(2)
    expect(rows()[1]).toHaveValue('Sush')
  })

  it('adds a row with the add button', async () => {
    const user = userEvent.setup()
    render(<TextOptionsEditor value={[{ label: 'Pizza' }]} onChange={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: /add option/i }))

    expect(rows()).toHaveLength(2)
  })

  it('stops adding rows once the limit is reached', async () => {
    const user = userEvent.setup()
    render(
      <TextOptionsEditor
        value={[{ label: 'Pizza' }, { label: 'Sushi' }]}
        onChange={vi.fn()}
        max={2}
      />,
    )

    await user.click(rows()[0] as HTMLInputElement)
    await user.keyboard('{Enter}')

    expect(rows()).toHaveLength(2)
    expect(screen.getByRole('button', { name: /add option/i })).toBeDisabled()
  })
})
