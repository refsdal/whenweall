import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { VerifyWithToken } from '#/routes/verify-email'
import { verifyEmail } from '#/api/auth'

// `verify-email.tsx` also imports `useNavigate`/`Link` (used by sibling states, not
// `VerifyWithToken` itself) and calls `createFileRoute(...)` at module scope — all four must exist
// on the mock or the module fails to import.
vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => options,
  useRouter: () => ({ invalidate: vi.fn().mockResolvedValue(undefined) }),
  useNavigate: () => vi.fn(),
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}))

vi.mock('#/api/auth', () => ({
  verifyEmail: vi.fn(),
  requestEmailVerification: vi.fn(),
  signOut: vi.fn(),
}))

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('VerifyWithToken', () => {
  it('consumes the token from the query string and shows the success state', async () => {
    vi.mocked(verifyEmail).mockResolvedValue(undefined)

    render(<VerifyWithToken token="tok-123" />)

    // Starts in the verifying state — proves the call is not a no-op fire-and-forget the UI
    // ignores.
    expect(screen.getByRole('status')).toHaveTextContent(/verifying/i)

    expect(await screen.findByRole('heading', { name: /email verified/i })).toBeInTheDocument()
    expect(verifyEmail).toHaveBeenCalledExactlyOnceWith('tok-123')
  })

  it('shows the expired card when the token is rejected instead of a silent failure', async () => {
    vi.mocked(verifyEmail).mockRejectedValue(new Error('invalid or expired token'))

    render(<VerifyWithToken token="bad-token" />)

    expect(await screen.findByRole('heading', { name: /link expired/i })).toBeInTheDocument()
    expect(verifyEmail).toHaveBeenCalledExactlyOnceWith('bad-token')
  })
})
