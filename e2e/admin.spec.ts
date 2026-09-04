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

  // NOT IMPLEMENTED — blocked by a real navigation bug, not a test-selector problem: see the
  // task-11-14 report for full reproduction. `web/src/routes/admin/users.tsx` (the users LIST
  // route) is the TanStack Router *parent* of `admin/users.$id.tsx` (routeTree.gen.ts:
  // `AdminUsersIdRoute`'s `getParentRoute: () => AdminUsersRoute`), but `AdminUsers`'s component
  // never renders an `<Outlet />`. The child route's loader and API calls all fire correctly and
  // the URL genuinely changes to `/admin/users/$id` (confirmed both via a `<Link>` click and via a
  // hard `page.goto` to that exact URL), but nothing the child renders — ReasonDialog, the lock/
  // unlock/delete buttons, the lockReason field, the per-user audit history — ever appears; the
  // list page's own markup stays on screen throughout. This is a pre-existing bug (present since
  // the console's very first commit, 7d317a1, not a Go-rewrite regression) that has apparently
  // never been reached by a real click before now — every user of this admin console today is
  // clicking a user's name and silently getting the list page back. Fixing it is an app-code
  // change outside this task's remit (adding `<Outlet />` to `AdminUsers`, or un-nesting the route
  // via `users_.$id.tsx`), so this test was intentionally left out rather than committed failing
  // or papered over with a workaround that avoids exercising the real navigation.

  test('the jobs page lists a dead-lettered job and retrying it clears it from the list', async ({
    page,
    userStaffWithFailedJob,
  }) => {
    const { failedJobId } = userStaffWithFailedJob
    await signIn(page, userStaffWithFailedJob)
    await page.goto('/admin')
    await waitForHydration(page)

    // The dashboard counts it — scoped to the "Mail" section, same as the console-statistics test
    // above, since "Failed jobs" also appears as the nav tab and inside "View failed jobs".
    const mailSection = page
      .locator('section')
      .filter({ has: page.getByRole('heading', { name: 'Mail' }) })
    const failedCard = mailSection.getByText('Failed jobs', { exact: true }).locator('..')
    await expect(failedCard).not.toContainText(/^Failed jobs\s*0$/)

    // … and the Jobs tab (plan E) lists it: the seed embeds the job id in last_error so this row
    // is unambiguous even when other runs left dead letters behind.
    // `exact: true`: the bare name also (sub-string) matches "View failed jobs" below the stat.
    await page.getByRole('link', { name: 'Failed jobs', exact: true }).click()
    await page.waitForURL('**/admin/jobs')
    const row = page.getByRole('row').filter({ hasText: failedJobId })
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('mail:send')
    await expect(row).toContainText('e2e: seeded dead-lettered job')

    await row.getByRole('button', { name: /^Retry\b/ }).click()
    // Retry resets attempts to 0, so the row leaves the dead-letter view.
    await expect(row).toHaveCount(0)

    await page.goto('/admin/audit')
    await waitForHydration(page)
    await expect(
      page.getByRole('row').filter({ hasText: 'job.retry' }).filter({ hasText: failedJobId }),
    ).toHaveCount(1)
  })
})
