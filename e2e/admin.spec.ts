import { expect, signIn, test, waitForHydration } from './fixtures'

test.describe('admin console', () => {
  test('a staff user reaches the console and sees the statistics', async ({ page, userStaff }) => {
    await signIn(page, userStaff)
    await page.goto('/admin')
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible()
    await expect(page.getByText('Organizations', { exact: true })).toBeVisible()
  })

  test('a staff user can find a user by email', async ({ page, userStaff }) => {
    await signIn(page, userStaff)
    await page.goto('/admin/users')
    await waitForHydration(page)

    await page.getByLabel('Search by email or name').fill(userStaff.email)
    await page.getByLabel('Search by email or name').press('Enter')

    await expect(page.getByRole('cell', { name: userStaff.email })).toBeVisible()
  })

  // A 404 rather than a 403: there is no reason to confirm to a stranger that an admin area
  // exists here.
  test('an ordinary signed-in user gets a not-found, not a forbidden', async ({ page, user }) => {
    await signIn(page, user)
    await page.goto('/admin')
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: 'Admin' })).toHaveCount(0)
  })

  test('the admin link is hidden from an ordinary user', async ({ page, user }) => {
    await signIn(page, user)
    await page.goto('/dashboard')
    await waitForHydration(page)

    await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0)
  })
})
