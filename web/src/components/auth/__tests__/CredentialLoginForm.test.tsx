import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CredentialLoginForm } from '#/components/auth/CredentialLoginForm'
import { me, requestEmailVerification, signInWithCredential, signOut } from '#/api/auth'

vi.mock('#/api/auth', () => ({
  signInWithCredential: vi.fn(),
  me: vi.fn(),
  signOut: vi.fn(),
  requestEmailVerification: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
// Captcha off: the deployment under test has no Turnstile keys, so no token is ever demanded.
vi.mock('#/lib/captcha', () => ({
  useTurnstileSiteKey: () => '',
  useCaptchaEnabled: () => false,
}))

const verified = {
  id: '1', name: 'Ada', email: 'ada@example.com', emailVerified: true, locale: 'en' as const, hasPassword: true, isStaff: false,
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

async function fillAndSubmit(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
  await user.type(screen.getByLabelText(/password/i), 'correct horse battery')
  await user.click(screen.getByRole('button', { name: /^sign in$/i }))
}

describe('CredentialLoginForm', () => {
  it('signs in without a captcha token when captcha is disabled and reports the verified user', async () => {
    vi.mocked(signInWithCredential).mockResolvedValue(verified)
    vi.mocked(me).mockResolvedValue(verified)
    const onSignedIn = vi.fn()
    const user = userEvent.setup()
    render(<CredentialLoginForm onSignedIn={onSignedIn} />)

    await fillAndSubmit(user)

    expect(signInWithCredential).toHaveBeenCalledExactlyOnceWith('ada@example.com', 'correct horse battery', null)
    expect(onSignedIn).toHaveBeenCalledExactlyOnceWith(verified)
  })

  it('shows the unverified card with a resend button instead of continuing', async () => {
    const unverified = { ...verified, emailVerified: false }
    vi.mocked(signInWithCredential).mockResolvedValue(unverified)
    vi.mocked(me).mockResolvedValue(unverified)
    vi.mocked(requestEmailVerification).mockResolvedValue(undefined)
    const onSignedIn = vi.fn()
    const user = userEvent.setup()
    render(<CredentialLoginForm onSignedIn={onSignedIn} />)

    await fillAndSubmit(user)

    expect(onSignedIn).not.toHaveBeenCalled()
    expect(await screen.findByText(/isn't verified yet/i)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /resend verification email/i }))
    expect(requestEmailVerification).toHaveBeenCalledOnce()
  })

  it('treats a sign-in whose session is immediately refused as a locked account', async () => {
    vi.mocked(signInWithCredential).mockResolvedValue(verified)
    vi.mocked(me).mockResolvedValue(null) // AuthMountGuard answered 403 "account is locked"
    vi.mocked(signOut).mockResolvedValue(undefined)
    const onSignedIn = vi.fn()
    const user = userEvent.setup()
    render(<CredentialLoginForm onSignedIn={onSignedIn} />)

    await fillAndSubmit(user)

    expect(signOut).toHaveBeenCalledOnce()
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(await screen.findByText(/has been locked/i)).toBeInTheDocument()
  })
})
