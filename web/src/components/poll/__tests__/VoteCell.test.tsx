import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VoteCell } from '#/components/poll/VoteCell'

afterEach(() => cleanup())

describe('VoteCell', () => {
  it('cycles null → yes → if need be → no → null on click', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()

    const { rerender } = render(
      <VoteCell answer={null} onChange={onChange} allowIfNeedBe optionLabel="Tue 1 Sep" />,
    )
    await user.click(screen.getByRole('button'))
    expect(onChange).toHaveBeenLastCalledWith('yes')

    rerender(<VoteCell answer="yes" onChange={onChange} allowIfNeedBe optionLabel="Tue 1 Sep" />)
    await user.click(screen.getByRole('button'))
    expect(onChange).toHaveBeenLastCalledWith('ifneedbe')

    rerender(
      <VoteCell answer="ifneedbe" onChange={onChange} allowIfNeedBe optionLabel="Tue 1 Sep" />,
    )
    await user.click(screen.getByRole('button'))
    expect(onChange).toHaveBeenLastCalledWith('no')

    rerender(<VoteCell answer="no" onChange={onChange} allowIfNeedBe optionLabel="Tue 1 Sep" />)
    await user.click(screen.getByRole('button'))
    expect(onChange).toHaveBeenLastCalledWith(null)
  })

  it('skips if need be when the poll disallows it', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()

    render(<VoteCell answer="yes" onChange={onChange} allowIfNeedBe={false} optionLabel="Tue" />)
    await user.click(screen.getByRole('button'))

    expect(onChange).toHaveBeenCalledWith('no')
  })

  it('exposes the option and the current answer to assistive tech', () => {
    render(<VoteCell answer="yes" onChange={vi.fn()} allowIfNeedBe optionLabel="Tue 1 Sep" />)

    const cell = screen.getByRole('button', { name: /tue 1 sep/i })
    expect(cell).toHaveAccessibleName(/yes/i)
    expect(cell).toHaveAttribute('aria-pressed', 'true')
    expect(cell).toHaveAttribute('data-answer', 'yes')
  })

  it('describes an empty cell as unanswered and is not pressed', () => {
    render(<VoteCell answer={null} onChange={vi.fn()} allowIfNeedBe optionLabel="Tue 1 Sep" />)

    const cell = screen.getByRole('button')
    expect(cell).toHaveAccessibleName(/no answer/i)
    expect(cell).toHaveAttribute('aria-pressed', 'false')
    expect(cell).toHaveAttribute('data-answer', 'none')
  })

  it('renders read-only cells as an image, not a button', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()

    render(<VoteCell answer="no" onChange={onChange} allowIfNeedBe readOnly optionLabel="Tue" />)

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    const cell = screen.getByRole('img', { name: /tue/i })
    await user.click(cell)
    expect(onChange).not.toHaveBeenCalled()
  })
})
