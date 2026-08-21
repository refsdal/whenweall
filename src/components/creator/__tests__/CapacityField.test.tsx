import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CapacityField } from '#/components/creator/CapacityField'

afterEach(() => cleanup())

describe('CapacityField', () => {
  it('shows the current numeric value', () => {
    render(<CapacityField value={5} onChange={vi.fn()} />)

    expect(screen.getByRole('spinbutton')).toHaveValue(5)
  })

  it('calls onChange with the typed number', () => {
    const onChange = vi.fn()
    render(<CapacityField value={1} onChange={onChange} />)

    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '5' } })

    expect(onChange).toHaveBeenCalledWith(5)
  })

  it('does not call onChange for zero or an empty value', () => {
    const onChange = vi.fn()
    render(<CapacityField value={1} onChange={onChange} />)
    const input = screen.getByRole('spinbutton')

    fireEvent.change(input, { target: { value: '0' } })
    fireEvent.change(input, { target: { value: '' } })

    expect(onChange).not.toHaveBeenCalled()
  })

  it('does not call onChange above the maximum', () => {
    const onChange = vi.fn()
    render(<CapacityField value={1} onChange={onChange} />)

    fireEvent.change(screen.getByRole('spinbutton'), { target: { value: '20000' } })

    expect(onChange).not.toHaveBeenCalled()
  })

  it('toggling unlimited on calls onChange(null)', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<CapacityField value={3} onChange={onChange} />)

    await user.click(screen.getByRole('switch'))

    expect(onChange).toHaveBeenCalledWith(null)
  })

  it('shows the input disabled with an infinity placeholder when unlimited', () => {
    render(<CapacityField value={null} onChange={vi.fn()} />)
    const input = screen.getByRole('spinbutton')

    expect(input).toBeDisabled()
    expect(input).toHaveAttribute('placeholder', '∞')
  })

  it('toggling unlimited off restores a usable number', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<CapacityField value={null} onChange={onChange} />)

    await user.click(screen.getByRole('switch'))

    expect(onChange).toHaveBeenCalledWith(1)
  })

  it('reverts the visible text to the last committed value when blurred empty', () => {
    const onChange = vi.fn()
    render(<CapacityField value={5} onChange={onChange} />)
    const input = screen.getByRole('spinbutton')

    fireEvent.change(input, { target: { value: '' } })
    expect(input).toHaveValue(null)
    fireEvent.blur(input)

    expect(input).toHaveValue(5)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('reverts the visible text to the last committed value when blurred with an out-of-range number', () => {
    const onChange = vi.fn()
    render(<CapacityField value={3} onChange={onChange} />)
    const input = screen.getByRole('spinbutton')

    fireEvent.change(input, { target: { value: '20000' } })
    fireEvent.blur(input)

    expect(input).toHaveValue(3)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('associates the input and switch with their labels', () => {
    render(<CapacityField value={2} onChange={vi.fn()} id="my-capacity" />)

    expect(screen.getByLabelText(/capacity/i)).toBe(screen.getByRole('spinbutton'))
    expect(screen.getByLabelText(/unlimited/i)).toBe(screen.getByRole('switch'))
  })
})
