import { expect, signIn, test, waitForHydration } from './fixtures'

test.describe('auth', () => {
  test('signs in with a seeded user and lands on the dashboard', async ({ page, user }) => {
    await signIn(page, user)

    await expect(page).toHaveURL(/\/dashboard$/)
    await expect(page.getByRole('heading', { name: 'Your polls' })).toBeVisible()
  })

  test('shows an error for the wrong password', async ({ page, user }) => {
    await page.goto('/login')
    await waitForHydration(page)

    await page.locator('#login-email').fill(user.email)
    await page.locator('#login-password').fill('definitely-the-wrong-password')
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
    // The session is really gone, not just the header re-rendered.
    expect((await page.request.get('/api/v1/auth/me')).status()).toBe(401)
  })
})
