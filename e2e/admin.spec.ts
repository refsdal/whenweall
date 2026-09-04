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

  test('staff lock, unlock and delete a user with reasons; the user is signed out while locked; the audit log records each', async ({
    page,
    browser,
    userStaff,
    user,
  }) => {
    // The target signs in first so the lock can be seen taking effect on a live session.
    const targetContext = await browser.newContext()
    const targetPage = await targetContext.newPage()
    try {
      await signIn(targetPage, user)

      await signIn(page, userStaff)
      await page.goto('/admin/users')
      await waitForHydration(page)
      await page.getByLabel('Search by email or name').fill(user.email)
      await page.getByLabel('Search by email or name').press('Enter')
      await expect(page.getByRole('cell', { name: user.email })).toBeVisible()
      // Every seeded user defaults to the same display name ("E2E User" — fixtures.ts), so the
      // link is found by the row containing this spec's own email, not by name.
      const href = await page
        .getByRole('row')
        .filter({ hasText: user.email })
        .getByRole('link')
        .getAttribute('href')
      if (!href) throw new Error(`no admin/users/$id link in the row for ${user.email}`)
      const userId = href.split('/').filter(Boolean).pop()!
      await page.goto(href)
      await waitForHydration(page)
      await expect(page.getByRole('heading', { name: user.name })).toBeVisible()

      // Plan E's action buttons (e.g. "Lock account") and ReasonDialog confirm buttons (e.g.
      // "Lock") share only a leading verb, so the regexes are anchored on that.
      async function actWithReason(verb: RegExp, reason: string) {
        await page.getByRole('button', { name: verb }).click()
        const dialog = page.getByRole('dialog')
        await expect(dialog).toBeVisible()
        const confirm = dialog.getByRole('button', { name: verb })
        await expect(confirm).toBeDisabled() // a reason is mandatory
        await dialog.getByLabel('Why are you doing this?').fill(reason)
        await confirm.click()
        await expect(dialog).toBeHidden()
      }

      // --- lock ---
      await actWithReason(/^Lock\b/, 'e2e: lock')
      // "Locked" appears twice on the detail card once locked: the status badge, and the label of
      // the lockReason field below it — `.first()` sidesteps the ambiguity (same pattern as the
      // first test in this file, which scopes an equally-repeated string).
      await expect(page.getByText('Locked', { exact: true }).first()).toBeVisible()
      // The lockReason field's own value is the only element whose FULL text is exactly this
      // string — the audit-history entry further down the page also mentions the reason, but
      // quoted (“e2e: lock”), which `exact: true` does not match.
      await expect(page.getByText('e2e: lock', { exact: true })).toBeVisible()

      // The durable effect: the locked user's very next request is refused (not just a badge on
      // the admin screen), and the SPA renders them signed out. LockUser revokes the target's
      // existing Limen session outright (internal/admin/users.go's RevokeUserSessions call), so
      // the already-signed-in session this test is holding gets a plain 401 (no valid session)
      // rather than AuthMountGuard's 403 "account is locked" — that 403 is for the OTHER lock
      // scenario, a locked user completing a *fresh* sign-in while still locked (AuthMountGuard's
      // own doc comment in internal/auth/session.go), which is not this test's path.
      await expect
        .poll(async () => (await targetPage.request.get('/api/v1/auth/me')).status())
        .toBe(401)
      await targetPage.reload()
      await waitForHydration(targetPage)
      await expect(targetPage.getByRole('link', { name: 'Sign in' })).toBeVisible()
      await expect(targetPage.getByRole('button', { name: 'Account menu' })).toHaveCount(0)

      // --- unlock ---
      await actWithReason(/^Unlock\b/, 'e2e: unlock')
      await expect(page.getByText('Locked', { exact: true })).toHaveCount(0)
      // The durable effect: the account works again, not just that the badge is gone.
      await targetPage.context().clearCookies()
      await signIn(targetPage, user)
      await expect(targetPage.getByRole('heading', { name: 'Your polls' })).toBeVisible()

      // --- delete ---
      await actWithReason(/^Delete\b/, 'e2e: delete')
      await page.goto(`/admin/users/${userId}`)
      await waitForHydration(page)
      await expect(page.getByText('No account with that id.')).toBeVisible()

      // The durable effect: the credentials themselves are gone, not just the admin-side row.
      await targetPage.goto('/login')
      await waitForHydration(targetPage)
      await targetPage.locator('#login-email').fill(user.email)
      await targetPage.locator('#login-password').fill(user.password)
      await targetPage.getByRole('button', { name: 'Sign in' }).click()
      await expect(targetPage.getByText("That email or password isn't right.")).toBeVisible()

      // --- audit log ---
      // Scoped to `userId` (this spec's own seeded subject) *and* the typed reason, not merely the
      // action name, since /admin/audit is a single global, 100-row-capped table shared with every
      // other worker's admin actions across the whole suite. Both the action and the target id are
      // matched via their cells' `data-action`/`data-target` attributes (exact matches), not
      // `hasText` substring matching: "unlock-user" contains "lock-user", and users.id is a small
      // BIGSERIAL, so a substring like "20" can match another row's timestamp (e.g. "…2026…") or a
      // longer id under concurrent load — either would let a `hasText` filter alone false-positive.
      await page.goto('/admin/audit')
      await waitForHydration(page)
      for (const [action, reason] of [
        ['lock-user', 'e2e: lock'],
        ['unlock-user', 'e2e: unlock'],
        ['delete-user', 'e2e: delete'],
      ] as const) {
        await expect(
          page
            .getByRole('row')
            .filter({ has: page.locator(`[data-action="${action}"]`) })
            .filter({ has: page.locator(`[data-target="${userId}"]`) })
            .filter({ hasText: reason }),
        ).toHaveCount(1)
      }
    } finally {
      await targetContext.close()
    }
  })

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
