import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ShareSheet } from '#/components/poll/ShareSheet'

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

const URL_UNDER_TEST = 'https://samla.app/p/abcdefghijkl'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

beforeEach(() => {
  toast.success.mockReset()
  toast.error.mockReset()
})

function stubClipboard(impl: () => Promise<void>) {
  const writeText = vi.fn(impl)
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText },
    configurable: true,
    writable: true,
  })
  return writeText
}

describe('ShareSheet', () => {
  it('shows the poll link', () => {
    render(<ShareSheet url={URL_UNDER_TEST} open onOpenChange={vi.fn()} />)

    expect(screen.getByLabelText(/poll link/i)).toHaveValue(URL_UNDER_TEST)
  })

  it('copies the link and confirms with a toast', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard(() => Promise.resolve())

    render(<ShareSheet url={URL_UNDER_TEST} open onOpenChange={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: /copy link/i }))

    expect(writeText).toHaveBeenCalledWith(URL_UNDER_TEST)
    expect(toast.success).toHaveBeenCalledWith('Link copied.')
  })

  it('tells the visitor when copying fails', async () => {
    const user = userEvent.setup()
    stubClipboard(() => Promise.reject(new Error('denied')))

    render(<ShareSheet url={URL_UNDER_TEST} open onOpenChange={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: /copy link/i }))

    expect(toast.error).toHaveBeenCalledWith("Couldn't copy the link.")
  })

  it('renders nothing while closed', () => {
    render(<ShareSheet url={URL_UNDER_TEST} open={false} onOpenChange={vi.fn()} />)

    expect(screen.queryByLabelText(/poll link/i)).not.toBeInTheDocument()
  })
})
