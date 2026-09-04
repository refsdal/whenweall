import { expect, signIn, test, waitForHydration } from './fixtures'
import { countMail, extractLink, waitForMail } from './mailpit'

/**
 * The three journeys that only exist through the inbox. Plan A restored the verification gate:
 * an unverified account can sign in only far enough to be told to verify, and the resend button
 * on that card re-delivers the mail.
 */
test.describe('e-mail flows', () => {
  test('sign up, get told to verify, resend, follow the link, then sign in for real', async ({
    page,
    browser,
    request,
  }) => {
    const email = `verify-${Date.now()}@example.com`
    const password = 'correct horse battery staple'

    await page.goto('/signup')
    await waitForHydration(page)
    await page.locator('#signup-name').fill('Verified Person')
    await page.locator('#signup-email').fill(email)
    await page.locator('#signup-password').fill(password)
    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page.getByRole('heading', { name: 'Check your inbox' })).toBeVisible({
      timeout: 15_000,
    })

    const verifyMail = await waitForMail(request, email, { subject: /Verify your email address/ })
    expect(verifyMail.Text).toContain('Verified Person')

    // --- signing in before verifying: the unverified card, not the dashboard ---
    await page.goto('/login')
    await waitForHydration(page)
    await page.locator('#login-email').fill(email)
    await page.locator('#login-password').fill(password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText("Your email isn't verified yet.")).toBeVisible()
    await expect(page).not.toHaveURL(/\/dashboard/)

    // --- resend delivers a second copy ---
    await page.getByRole('button', { name: 'Resend verification email' }).click()
    await expect(page.getByText(/Verification email sent/)).toBeVisible()
    await expect
      .poll(() => countMail(request, email, /Verify your email address/), { timeout: 30_000 })
      .toBe(2)

    // --- the emailed link verifies the address ---
    const link = extractLink(verifyMail, '/verify-email')
    expect(link.searchParams.get('token')).toBeTruthy()
    await page.goto(link.pathname + link.search)
    await waitForHydration(page)
    await expect(page.getByRole('heading', { name: 'Email verified' })).toBeVisible()

    // --- a clean context proves the credentials now sign in all the way to the dashboard ---
    const fresh = await browser.newContext()
    const freshPage = await fresh.newPage()
    try {
      await signIn(freshPage, { email, password })
      await expect(freshPage.getByRole('heading', { name: 'Your polls' })).toBeVisible()
      const me = (await (await freshPage.request.get('/api/v1/auth/me')).json()) as {
        user: { emailVerified: boolean; name: string }
      }
      expect(me.user.emailVerified).toBe(true)
      expect(me.user.name).toBe('Verified Person')
    } finally {
      await fresh.close()
    }
  })

  test('forgot password: the emailed link sets a new password that signs in', async ({
    page,
    request,
    user,
  }) => {
    await page.goto('/forgot-password')
    await waitForHydration(page)
    await page.locator('#forgot-email').fill(user.email)
    await page.getByRole('button', { name: 'Send reset link' }).click()
    await expect(page.getByRole('heading', { name: 'Check your inbox' })).toBeVisible()

    const resetMail = await waitForMail(request, user.email, { subject: /Reset your password/ })
    const link = extractLink(resetMail, '/reset-password')
    expect(link.searchParams.get('token')).toBeTruthy()

    const newPassword = `Fresh-${Date.now()}-passphrase`
    await page.goto(link.pathname + link.search)
    await waitForHydration(page)
    await page.locator('#reset-password').fill(newPassword)
    await page.getByRole('button', { name: 'Reset password' }).click()
    await expect(page.getByRole('heading', { name: 'Password updated' })).toBeVisible()

    // The old password is dead …
    await page.goto('/login')
    await waitForHydration(page)
    await page.locator('#login-email').fill(user.email)
    await page.locator('#login-password').fill(user.password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText("That email or password isn't right.")).toBeVisible()

    // … and the new one works.
    await signIn(page, { email: user.email, password: newPassword })
    await expect(page.getByRole('heading', { name: 'Your polls' })).toBeVisible()
  })
})
