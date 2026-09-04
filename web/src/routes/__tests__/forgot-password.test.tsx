import type { ReactNode } from 'react'
import { useState as useMockTurnstileState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ForgotPasswordPage } from '#/routes/forgot-password'
import { ApiError } from '#/api/client'
import { requestPasswordReset } from '#/api/auth'

// forgot-password.tsx calls createFileRoute(...) at module scope and imports Link for its success
// card — ForgotPasswordPage itself uses neither Route.useSearch nor Route.useRouteContext, so this
// mirrors verify-email.test.tsx's minimal mock rather than needing a fuller Route stand-in.
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => options,
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

// Same lazy-useState-per-mount trick as CredentialLoginForm.captcha.test.tsx: proves a genuine
// widget remount happened (a fresh token), not just a re-render of the same instance.
let turnstileMountCount = 0
vi.mock('@marsidev/react-turnstile', () => ({
  Turnstile: ({ onSuccess }: { onSuccess: (token: string) => void }) => {
    const [token] = useMockTurnstileState(() => `tok-${++turnstileMountCount}`)
    return (
      <button type="button" onClick={() => onSuccess(token)}>
        mock turnstile
      </button>
    )
  },
}))

vi.mock('#/api/auth', () => ({
  requestPasswordReset: vi.fn(),
}))
const toastError = vi.fn()
vi.mock('sonner', () => ({ toast: { error: (...args: unknown[]) => toastError(...args) } }))
vi.mock('#/lib/captcha', () => ({
  useTurnstileSiteKey: () => 'site-key',
  useCaptchaEnabled: () => true,
}))

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  turnstileMountCount = 0
})

async function fillAndSolve(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
  await user.click(screen.getByRole('button', { name: 'mock turnstile' }))
}

describe('ForgotPasswordPage captcha handling', () => {
  it('shows the success card on an ordinary failure, without leaking whether the email exists', async () => {
    vi.mocked(requestPasswordReset).mockRejectedValueOnce(new ApiError('not_found', 'not found', 404))
    const user = userEvent.setup()
    render(<ForgotPasswordPage />)

    await fillAndSolve(user)
    await user.click(screen.getByRole('button', { name: /reset link/i }))

    expect(await screen.findByText(/ada@example\.com/i)).toBeInTheDocument()
    expect(toastError).not.toHaveBeenCalled()
  })

  // Reviewer's finding 1: a Turnstile token is single-use, and this endpoint's own captcha check
  // runs before Limen ever looks at whether the email is registered — so surfacing captcha_failed
  // here leaks nothing an attacker could use, and silently claiming "email sent" for a request
  // that never went through would just leave the user waiting forever with no way to know why.
  it('surfaces captcha_failed distinctly and lets the user retry with a fresh token', async () => {
    vi.mocked(requestPasswordReset).mockRejectedValueOnce(
      new ApiError('captcha_failed', 'captcha verification failed', 403),
    )
    vi.mocked(requestPasswordReset).mockResolvedValueOnce(undefined)
    const user = userEvent.setup()
    render(<ForgotPasswordPage />)

    await fillAndSolve(user)
    await user.click(screen.getByRole('button', { name: /reset link/i }))

    await waitFor(() => expect(toastError).toHaveBeenCalledOnce())
    // Still on the form, not the success card — the request never actually reached the server.
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument()
    expect(requestPasswordReset).toHaveBeenNthCalledWith(1, 'ada@example.com', 'tok-1')

    await user.click(await screen.findByRole('button', { name: 'mock turnstile' }))
    await user.click(screen.getByRole('button', { name: /reset link/i }))

    await waitFor(() => expect(requestPasswordReset).toHaveBeenCalledTimes(2))
    expect(requestPasswordReset).toHaveBeenNthCalledWith(2, 'ada@example.com', 'tok-2')
    expect(await screen.findByText(/ada@example\.com/i)).toBeInTheDocument()
  })
})
