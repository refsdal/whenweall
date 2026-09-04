import { expect, signIn, test, waitForHydration } from './fixtures'

test.describe('admin console', () => {
  test('a staff user reaches the console and sees the statistics', async ({ page, userStaff }) => {
    await signIn(page, userStaff)
    await page.goto('/admin')
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: 'Admin' })).toBeVisible()
    await expect(page.getByText('Organizations', { exact: true })).toBeVisible()
    await expect(page.getByText('Mail queue depth')).toBeVisible()
    // Scoped to the "Mail" section with `exact: true`: the bare text "Failed jobs" also matches
    // the nav tab (AdminLayout's own `<nav>`, outside this page's content) and, as a
    // case-insensitive substring, the "View failed jobs" link right below the stat — three
    // legitimate uses of the same words on one page, not a product bug.
    const mailSection = page
      .locator('section')
      .filter({ has: page.getByRole('heading', { name: 'Mail' }) })
    await expect(mailSection.getByText('Failed jobs', { exact: true })).toBeVisible()
  })

  test('a staff user can find a user by email', async ({ page, userStaff }) => {
    await signIn(page, userStaff)
    await page.goto('/admin/users')
    await waitForHydration(page)

    await page.getByLabel('Search by email or name').fill(userStaff.email)
    await page.getByLabel('Search by email or name').press('Enter')

    await expect(page.getByRole('cell', { name: userStaff.email })).toBeVisible()
  })

  // The SPA renders its generic not-found card rather than a "forbidden" page: there is no reason
  // to confirm to a stranger that an admin area exists here. The API underneath is the real gate
  // and answers 403 (auth.Service.RequireStaff) to a signed-in non-staff caller, 401 to nobody.
  test('an ordinary signed-in user gets a not-found page and a 403 from the admin API', async ({
    page,
    request,
    user,
  }) => {
    await signIn(page, user)
    await page.goto('/admin')
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: "We can't find that page" })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Admin' })).toHaveCount(0)

    const stats = await page.request.get('/api/v1/admin/stats')
    expect(stats.status()).toBe(403)
    const anonymous = await request.get('/api/v1/admin/stats')
    expect(anonymous.status()).toBe(401)
  })

  test('the admin link is hidden from an ordinary user', async ({ page, user }) => {
    await signIn(page, user)
    await page.goto('/dashboard')
    await waitForHydration(page)

    await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0)
  })
})
