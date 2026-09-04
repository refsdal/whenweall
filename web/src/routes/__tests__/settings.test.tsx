import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DeleteAccountSection } from '#/routes/settings'
import { ApiError } from '#/api/client'
import { deleteOwnAccount } from '#/api/auth'

const invalidateMock = vi.fn().mockResolvedValue(undefined)
const navigateMock = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => options,
  useRouter: () => ({ invalidate: invalidateMock }),
  useNavigate: () => navigateMock,
}))

vi.mock('#/api/auth', () => ({
  deleteOwnAccount: vi.fn(),
  myOrgRoles: vi.fn(),
  updateProfile: vi.fn(),
}))

const toastError = vi.fn()
const toastSuccess = vi.fn()
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastError(...args),
    success: (...args: unknown[]) => toastSuccess(...args),
  },
}))

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('DeleteAccountSection', () => {
  it('surfaces an error toast and leaves the dialog open when the password is refused', async () => {
    vi.mocked(deleteOwnAccount).mockRejectedValue(new ApiError('invalid_password', 'invalid password', 403))
    const user = userEvent.setup()

    render(<DeleteAccountSection hasPassword />)
    await user.click(screen.getByRole('button', { name: /^delete account$/i }))
    const confirm = screen.getAllByRole('button', { name: /^delete account$/i }).at(-1)!
    await user.type(screen.getByLabelText(/^password$/i), 'wrong-password')
    await user.click(confirm)

    // The dialog stays open — the password field is still there for another try.
    expect(await screen.findByLabelText(/^password$/i)).toBeInTheDocument()
    expect(toastError).toHaveBeenCalledOnce()
    expect(toastSuccess).not.toHaveBeenCalled()
    expect(navigateMock).not.toHaveBeenCalled()
    expect(invalidateMock).not.toHaveBeenCalled()
  })

  it('navigates away and toasts success when the password is accepted', async () => {
    vi.mocked(deleteOwnAccount).mockResolvedValue(undefined)
    const user = userEvent.setup()

    render(<DeleteAccountSection hasPassword />)
    await user.click(screen.getByRole('button', { name: /^delete account$/i }))
    const confirm = screen.getAllByRole('button', { name: /^delete account$/i }).at(-1)!
    await user.type(screen.getByLabelText(/^password$/i), 'correct-password')
    await user.click(confirm)

    await vi.waitFor(() => expect(navigateMock).toHaveBeenCalledOnce())
    expect(toastSuccess).toHaveBeenCalledOnce()
    expect(toastError).not.toHaveBeenCalled()
    expect(invalidateMock).toHaveBeenCalledOnce()
    expect(navigateMock).toHaveBeenCalledExactlyOnceWith({ to: '/' })
  })
})
