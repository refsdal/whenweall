import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DataExportSection, DeleteAccountSection } from '#/routes/settings'
import { ApiError } from '#/api/client'
import { deleteOwnAccount, exportMyData } from '#/api/auth'

const invalidateMock = vi.fn().mockResolvedValue(undefined)
const navigateMock = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => options,
  useRouter: () => ({ invalidate: invalidateMock }),
  useNavigate: () => navigateMock,
}))

vi.mock('#/api/auth', () => ({
  deleteOwnAccount: vi.fn(),
  exportMyData: vi.fn(),
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

describe('DataExportSection', () => {
  it('hands the browser the export as a file named by the server', async () => {
    vi.mocked(exportMyData).mockResolvedValue({
      blob: new Blob(['{}'], { type: 'application/json' }),
      filename: 'whenweall-export-2026-10-31.json',
    })
    const createObjectURL = vi.fn(() => 'blob:export')
    const revokeObjectURL = vi.fn()
    Object.assign(URL, { createObjectURL, revokeObjectURL })
    const clicked: HTMLAnchorElement[] = []
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicked.push(this)
    })
    const user = userEvent.setup()

    render(<DataExportSection />)
    await user.click(screen.getByRole('button', { name: /download my data/i }))

    expect(clicked).toHaveLength(1)
    expect(clicked[0]?.download).toBe('whenweall-export-2026-10-31.json')
    expect(clicked[0]?.getAttribute('href')).toBe('blob:export')
    await vi.waitFor(() => expect(revokeObjectURL).toHaveBeenCalledWith('blob:export'))
    expect(toastError).not.toHaveBeenCalled()
    click.mockRestore()
  })

  it('explains the hourly limit instead of saving an error as the file', async () => {
    vi.mocked(exportMyData).mockRejectedValue(new ApiError('rate_limited', 'rate limited', 429))
    const user = userEvent.setup()

    render(<DataExportSection />)
    await user.click(screen.getByRole('button', { name: /download my data/i }))

    expect(toastError).toHaveBeenCalledWith(
      expect.stringMatching(/several times in the last hour/i),
    )
    expect(screen.getByRole('button', { name: /download my data/i })).toBeEnabled()
  })
})
