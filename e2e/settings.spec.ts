import { expect, signIn, test, waitForHydration } from './fixtures'

type Me = { user: { name: string; locale: string } }

async function me(page: import('@playwright/test').Page): Promise<Me['user']> {
  const response = await page.request.get('/api/v1/auth/me')
  expect(response.status(), 'GET /api/v1/auth/me').toBe(200)
  return ((await response.json()) as Me).user
}

test('renaming shows in the header, the locale is stored on the profile, and deleting the account (password re-check) signs it out for good', async ({
  page,
  browser,
  user,
}) => {
  await signIn(page, user)
  await page.goto('/settings')
  await waitForHydration(page)
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()

  // --- display name ---
  const newName = `Renamed ${Date.now()}`
  // "Name" (settings_name_label); the handle field is labelled "Handle", so `exact` disambiguates.
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(newName)
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Name updated.')).toBeVisible()
  await expect.poll(async () => (await me(page)).name).toBe(newName)

  await page.getByRole('button', { name: 'Account menu' }).click()
  await expect(page.getByRole('menu')).toContainText(newName)
  await page.keyboard.press('Escape')

  // --- locale: the switcher on /settings persists to the profile, not just the cookie ---
  const languageGroup = page.getByRole('main').getByRole('group', { name: 'Language' })
  await languageGroup.getByRole('button', { name: 'NO' }).click()
  await expect(page.locator('html')).toHaveAttribute('lang', 'nb', { timeout: 10_000 })
  await expect.poll(async () => (await me(page)).locale).toBe('nb')

  // A context with NO locale cookie, signed in as the same person, reads `nb` off the profile —
  // that is what makes e-mails and a second device follow the choice.
  const fresh = await browser.newContext()
  const freshPage = await fresh.newPage()
  try {
    await signIn(freshPage, user)
    const profile = await me(freshPage)
    expect(profile.locale).toBe('nb')
    expect(profile.name).toBe(newName)
  } finally {
    await fresh.close()
  }

  // Back to English so the danger-zone labels below match the en.json copy this spec asserts on.
  await page.getByRole('main').getByRole('group', { name: 'Språk' }).getByRole('button', { name: 'EN' }).click()
  await expect(page.locator('html')).toHaveAttribute('lang', 'en', { timeout: 10_000 })
  await expect.poll(async () => (await me(page)).locale).toBe('en')

  // --- delete account: wrong password is refused, the right one deletes ---
  await page.getByRole('button', { name: 'Delete account' }).click()
  const dialog = page.getByRole('dialog', { name: 'Delete your account?' })
  await expect(dialog).toBeVisible()
  await dialog.getByLabel('Password').fill('not-the-password-at-all')
  await dialog.getByRole('button', { name: 'Delete account' }).click()
  await expect(
    page.getByText("Couldn't delete your account. Check your password and try again."),
  ).toBeVisible()
  await expect(dialog).toBeVisible()

  await dialog.getByLabel('Password').fill(user.password)
  await dialog.getByRole('button', { name: 'Delete account' }).click()
  await expect(page.getByText('Account deleted.')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Sign in' })).toBeVisible()
  expect((await page.request.get('/api/v1/auth/me')).status()).toBe(401)

  // The credentials are gone, not just the session.
  await page.goto('/login')
  await waitForHydration(page)
  await page.locator('#login-email').fill(user.email)
  await page.locator('#login-password').fill(user.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText("That email or password isn't right.")).toBeVisible()
  await expect(page).toHaveURL(/\/login/)
})
