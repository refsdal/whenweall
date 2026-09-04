import type { ReactNode } from 'react'
import { useState as useMockTurnstileState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SignupPage } from '#/routes/signup'
import { ApiError } from '#/api/client'
import { signUpWithCredential } from '#/api/auth'

// signup.tsx reads `Route.useSearch()`/`Route.useRouteContext()` directly (unlike
// forgot-password.tsx/verify-email.tsx's VerifyWithToken), so the mocked `createFileRoute` has to
// hand back an object exposing both, not just the raw options literal.
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => ({
    ...(options as Record<string, unknown>),
    useSearch: () => ({ next: undefined }),
    useRouteContext: () => ({
      publicConfig: { googleEnabled: false, oidcEnabled: false, oidcName: null },
    }),
  }),
  redirect: (x: unknown) => {
    throw x
  },
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

// Same lazy-useState-per-mount trick as CredentialLoginForm.captcha.test.tsx/
// forgot-password.test.tsx: proves a genuine widget remount happened (a fresh token).
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
  signUpWithCredential: vi.fn(),
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
  await user.type(screen.getByLabelText(/name/i), 'Ada Lovelace')
  await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
  await user.type(screen.getByLabelText(/^password$/i), 'correct horse battery staple')
  await user.click(screen.getByRole('button', { name: 'mock turnstile' }))
}

// Reviewer's finding 1: a Turnstile token is single-use, so a duplicate-email rejection on the
// first signup attempt still burns it; without a reset, a second attempt with a different email
// would silently resend the SAME (already-redeemed) token and fail captcha_failed forever.
describe('SignupPage captcha handling', () => {
  it('resets the burned Turnstile token after a rejected signup so a retry uses a fresh one', async () => {
    vi.mocked(signUpWithCredential).mockRejectedValueOnce(
      new ApiError('conflict', 'email already exists', 409),
    )
    vi.mocked(signUpWithCredential).mockResolvedValueOnce(undefined as never)
    const user = userEvent.setup()
    render(<SignupPage />)

    await fillAndSolve(user)
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => expect(toastError).toHaveBeenCalledOnce())
    expect(signUpWithCredential).toHaveBeenNthCalledWith(
      1, 'ada@example.com', 'correct horse battery staple', 'Ada Lovelace', 'tok-1',
    )

    await user.click(await screen.findByRole('button', { name: 'mock turnstile' }))
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => expect(signUpWithCredential).toHaveBeenCalledTimes(2))
    expect(signUpWithCredential).toHaveBeenNthCalledWith(
      2, 'ada@example.com', 'correct horse battery staple', 'Ada Lovelace', 'tok-2',
    )
    expect(await screen.findByText(/ada@example\.com/i)).toBeInTheDocument()
  })
})
