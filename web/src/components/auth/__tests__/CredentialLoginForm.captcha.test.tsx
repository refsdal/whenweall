import { useState as useMockTurnstileState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CredentialLoginForm } from '#/components/auth/CredentialLoginForm'
import { ApiError } from '#/api/client'
import { me, signInWithCredential } from '#/api/auth'

// Every mounted widget instance hands back a token unique to that MOUNT ("tok-1", "tok-2", ...) —
// a lazy useState initializer, not a plain counter increment in the render body, so a re-render of
// the SAME instance (typing in another field, a submit's own state changes) keeps its token while
// a genuine remount (TurnstileField's `key` changing) gets a fresh one. The point of this test is
// proving a real remount happens after a rejected submit, so the token itself has to prove it, not
// just its presence.
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
  signInWithCredential: vi.fn(),
  me: vi.fn(),
  signOut: vi.fn(),
  requestEmailVerification: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }))
vi.mock('#/lib/captcha', () => ({
  useTurnstileSiteKey: () => 'site-key',
  useCaptchaEnabled: () => true,
}))

const verified = {
  id: '1', name: 'Ada', email: 'ada@example.com', emailVerified: true, locale: 'en' as const, hasPassword: true, isStaff: false,
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  turnstileMountCount = 0
})

// Reviewer's finding 1: authCaptchaMiddleware verifies AND redeems a Turnstile token before the
// request ever reaches Limen, so a wrong password on the first attempt burns the token; without a
// reset, the second attempt would silently resend the SAME (already-redeemed) token and fail
// captcha_failed forever, no matter how many times the user retries with the right password.
describe('CredentialLoginForm captcha handling', () => {
  it('remounts the Turnstile widget after a rejected sign-in so a retry uses a fresh token', async () => {
    vi.mocked(signInWithCredential).mockRejectedValueOnce(
      new ApiError('unauthenticated', 'invalid credential', 401),
    )
    vi.mocked(signInWithCredential).mockResolvedValueOnce(verified)
    vi.mocked(me).mockResolvedValue(verified)
    const onSignedIn = vi.fn()
    const user = userEvent.setup()
    render(<CredentialLoginForm onSignedIn={onSignedIn} />)

    await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
    await user.type(screen.getByLabelText(/password/i), 'first-try')
    await user.click(screen.getByRole('button', { name: 'mock turnstile' }))
    await user.click(screen.getByRole('button', { name: /^sign in$/i }))

    await waitFor(() => expect(signInWithCredential).toHaveBeenCalledTimes(1))
    expect(signInWithCredential).toHaveBeenNthCalledWith(1, 'ada@example.com', 'first-try', 'tok-1')

    // A second solve must produce a genuinely new token — proving the widget was actually reset,
    // not just left showing the same (now-useless) solved state.
    await user.click(await screen.findByRole('button', { name: 'mock turnstile' }))
    await user.click(screen.getByRole('button', { name: /^sign in$/i }))

    await waitFor(() => expect(signInWithCredential).toHaveBeenCalledTimes(2))
    expect(signInWithCredential).toHaveBeenNthCalledWith(2, 'ada@example.com', 'first-try', 'tok-2')
    expect(onSignedIn).toHaveBeenCalledExactlyOnceWith(verified)
  })
})
