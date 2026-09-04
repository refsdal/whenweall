import { APP_URL } from './e2e-env'
import { expect, pickTwoCalendarDays, signIn, test, waitForHydration } from './fixtures'
import { extractLink, waitForMail } from './mailpit'

test('an owner invites a teammate; the invitee accepts from the mail, is switched into the org and creates a poll there', async ({
  page,
  browser,
  request,
  user,
  userWithPoll,
}) => {
  const owner = userWithPoll
  const invitee = user

  await signIn(page, owner)

  // There is no invite UI in the SPA yet (only the accept side), so the owner invites through
  // Limen's own route, exactly the way the old billing.spec did. `page.request` carries the
  // owner's session cookies; the Origin header is what our CheckOrigin middleware requires on any
  // mutating /api/v1 call (a browser fetch() would add it on its own).
  const invite = await page.request.post('/api/v1/auth/organizations/invitations', {
    headers: { origin: APP_URL },
    data: { email: invitee.email, role: 'member' },
  })
  expect(invite.ok(), await invite.text()).toBe(true)

  const mail = await waitForMail(request, invitee.email, { subject: /invited you to .+ on whenweall$/ })
  const orgName = /invited you to (.+) on whenweall$/.exec(mail.Subject)![1]!
  expect(mail.Text).toContain(owner.name)
  const acceptLink = extractLink(mail, '/accept-invitation/')

  const inviteeContext = await browser.newContext()
  const inviteePage = await inviteeContext.newPage()
  try {
    await signIn(inviteePage, invitee)

    // Before accepting: the invitee's active org is their own personal one, not the inviter's.
    const before = (await (await inviteePage.request.get('/api/v1/auth/organizations/active')).json()) as {
      name: string
    }
    expect(before.name).not.toBe(orgName)

    await inviteePage.goto(acceptLink.pathname)
    await waitForHydration(inviteePage)
    await expect(inviteePage.getByRole('heading', { name: 'Join the organization' })).toBeVisible()
    await inviteePage.getByRole('button', { name: 'Accept invitation' }).click()
    await inviteePage.waitForURL('**/dashboard')

    // Plan A: accepting switches the active organization to the one just joined …
    const after = (await (await inviteePage.request.get('/api/v1/auth/organizations/active')).json()) as {
      name: string
    }
    expect(after.name).toBe(orgName)

    // … and the account menu's org switcher lists the joined org (by name) alongside the personal
    // one. The switcher lives in a submenu under the Account menu dropdown that only appears once
    // there is more than one org to choose from, and its items are not mounted until that submenu
    // itself is opened. The submenu's own trigger is labelled with the *active* org's name rather
    // than a fixed "Organizations" string (UserMenu.tsx: `activeOrg?.name ?? m.nav_organizations()`)
    // — since the active org is now the joined one, the trigger itself already reads `orgName`.
    // The active-state markup is otherwise left unfixed by plan A, so the API check above is the
    // authority on "active"; this only asserts the switcher knows about both orgs.
    await inviteePage.getByRole('button', { name: 'Account menu' }).click()
    const menu = inviteePage.getByRole('menu').first()
    await menu.getByRole('menuitem', { name: orgName }).click()
    const submenu = inviteePage.getByRole('menu').last()
    await expect(submenu.getByText(orgName)).toBeVisible()
    await expect(submenu.getByText(before.name)).toBeVisible()
    await inviteePage.keyboard.press('Escape')
    await inviteePage.keyboard.press('Escape')

    // The joined org's existing poll is now on the invitee's dashboard.
    await inviteePage.goto('/dashboard')
    await waitForHydration(inviteePage)
    await expect(
      inviteePage
        .locator('[data-testid="poll-card"]')
        .filter({ has: inviteePage.getByText('Seeded test poll', { exact: true }) }),
    ).toHaveCount(1)

    // A poll the invitee creates now belongs to the joined org …
    const title = `Team poll ${Date.now()}`
    await inviteePage.goto('/new')
    await waitForHydration(inviteePage)
    await inviteePage.locator('#creator-title').fill(title)
    await inviteePage.getByRole('button', { name: 'Next', exact: true }).click()
    await pickTwoCalendarDays(inviteePage)
    await inviteePage.getByRole('button', { name: 'Next', exact: true }).click()
    await inviteePage.getByRole('button', { name: 'Create poll' }).click()
    await inviteePage.waitForURL(/\/p\/[^/?]+/)
  } finally {
    await inviteeContext.close()
  }

  // … so the owner sees it on their own dashboard (polls are listed per organization).
  await page.goto('/dashboard')
  await waitForHydration(page)
  await expect(
    page
      .locator('[data-testid="poll-card"]')
      .filter({ has: page.getByText(/^Team poll \d+$/) }),
  ).toHaveCount(1)
})
