import { expect, signIn, test, waitForTurnstile } from './fixtures'

test.describe('auth', () => {
  test('signing up shows the check-your-inbox screen', async ({ page }) => {
    await page.goto('/signup')

    await page.locator('#signup-name').fill('New Person')
    await page.locator('#signup-email').fill(`signup-${Date.now()}@example.com`)
    await page.locator('#signup-password').fill('correct horse battery staple')
    await waitForTurnstile(page)
    await page.getByRole('button', { name: 'Create account' }).click()

    await expect(page.getByRole('heading', { name: 'Check your inbox' })).toBeVisible({
      timeout: 15_000,
    })
  })

  test('signs in with a seeded user and lands on the dashboard', async ({ page, user }) => {
    await signIn(page, user)

    await expect(page).toHaveURL(/\/dashboard$/)
    await expect(page.getByRole('heading', { name: 'Your polls' })).toBeVisible()
  })

  test('shows an error for the wrong password', async ({ page, user }) => {
    await page.goto('/login')

    await page.locator('#login-email').fill(user.email)
    await page.locator('#login-password').fill('definitely-the-wrong-password')
    await waitForTurnstile(page)
    await page.getByRole('button', { name: 'Sign in' }).click()

    await expect(page.getByText("That email or password isn't right.")).toBeVisible()
    await expect(page).toHaveURL(/\/login/)
  })

  test('signs out and returns to the landing page', async ({ page, user }) => {
    await signIn(page, user)

    await page.getByRole('button', { name: 'Account menu' }).click()
    await page.getByRole('menuitem', { name: 'Sign out' }).click()

    await expect(page).toHaveURL('/')
    await expect(page.getByRole('link', { name: 'Sign in' })).toBeVisible()
  })

  test('settings page shows the passkeys section', async ({ page, user }) => {
    await signIn(page, user)
    await page.goto('/settings')

    await expect(page.getByRole('heading', { name: 'Passkeys' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Add passkey' })).toBeVisible()
  })
})
