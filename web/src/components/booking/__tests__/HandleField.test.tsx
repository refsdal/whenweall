import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HandleField } from '#/components/booking/HandleField'
import { ApiError } from '#/api/client'

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function renderField(currentHandle: string | null = null) {
  const onSave = vi.fn().mockResolvedValue(undefined)
  render(
    <HandleField currentHandle={currentHandle} appUrl="https://whenweall.com" onSave={onSave} />,
  )
  return { onSave }
}

function handleInput(): HTMLElement {
  return screen.getByLabelText(/handle/i)
}

describe('HandleField', () => {
  it('shows the public prefix the handle is appended to', () => {
    renderField()

    expect(screen.getByText('whenweall.com/book/')).toBeInTheDocument()
  })

  it('seeds the field with the current handle and cannot re-save it unchanged', () => {
    renderField('anders')

    expect(handleInput()).toHaveValue('anders')
    expect(screen.getByRole('button', { name: /save handle/i })).toBeDisabled()
  })

  it('keeps Save disabled while the field is empty', () => {
    renderField()

    expect(screen.getByRole('button', { name: /save handle/i })).toBeDisabled()
  })

  it('shows an error and disables Save for a handle that is too short', async () => {
    const user = userEvent.setup()
    renderField()

    await user.type(handleInput(), 'ab')

    expect(screen.getByText(/3–30 lowercase letters/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save handle/i })).toBeDisabled()
  })

  it('shows an error for a handle with illegal characters', async () => {
    const user = userEvent.setup()
    renderField()

    await user.type(handleInput(), 'Anders Ro')

    expect(screen.getByText(/3–30 lowercase letters/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save handle/i })).toBeDisabled()
  })

  it('enables Save for a valid handle and reports it once', async () => {
    const user = userEvent.setup()
    const { onSave } = renderField()

    await user.type(handleInput(), 'anders-ro')

    const save = screen.getByRole('button', { name: /save handle/i })
    expect(screen.queryByText(/3–30 lowercase letters/i)).not.toBeInTheDocument()
    expect(save).toBeEnabled()

    await user.click(save)

    expect(onSave).toHaveBeenCalledExactlyOnceWith('anders-ro')
    expect(toast.success).toHaveBeenCalled()
  })

  it('previews the link the handle will produce', async () => {
    const user = userEvent.setup()
    renderField()

    await user.type(handleInput(), 'anders-ro')

    expect(screen.getByText(/whenweall\.com\/book\/anders-ro/)).toBeInTheDocument()
  })

  it('surfaces a taken handle as an inline error', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockRejectedValue(new ApiError('handle_taken', 'taken', 409))
    render(<HandleField currentHandle={null} appUrl="https://whenweall.com" onSave={onSave} />)

    await user.type(handleInput(), 'taken-one')
    await user.click(screen.getByRole('button', { name: /save handle/i }))

    expect(await screen.findByText(/that handle is taken/i)).toBeInTheDocument()
    expect(toast.success).not.toHaveBeenCalled()
  })
})
