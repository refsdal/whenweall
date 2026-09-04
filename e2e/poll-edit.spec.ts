import { expect, signIn, test, waitForHydration } from './fixtures'

test('the owner edits a voted poll (confirming the lost vote), adds a day, then a past deadline closes it for a guest', async ({
  page,
  browser,
  userWithPoll,
}) => {
  const { pollId } = userWithPoll
  const pollPath = `/p/${pollId}`

  // --- a guest votes yes on both seeded options ---
  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(pollPath)
    await waitForHydration(guestPage)
    await guestPage.getByTestId('add-yourself-row').getByLabel('Your name').fill('Edit Guest')
    const cells = guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await expect(cells).toHaveCount(2)
    await cells.nth(0).click()
    await cells.nth(1).click()
    await guestPage.getByRole('button', { name: 'Save my answer' }).click()
    const guestRow = guestPage.locator('[data-testid^="participant-row-"]').filter({ hasText: 'You' })
    await expect(guestRow.locator('span[data-answer="yes"]')).toHaveCount(2)

    // --- the owner removes the first day and adds another ---
    await signIn(page, userWithPoll)
    await page.goto(`${pollPath}/edit`)
    await waitForHydration(page)
    await expect(page.getByTestId('poll-editor')).toBeVisible()
    await expect(page.getByText('2 options', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: /^Remove / }).first().click()
    await expect(page.getByText('1 option', { exact: true })).toBeVisible()

    // A day two months out can never coincide with the seeded options (2–3 days ahead), so clicking
    // it always ADDS a day rather than toggling one off.
    const calendar = page.locator('[data-slot="calendar"]')
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
    await calendar.locator('button[data-day]:not([disabled])').first().click()
    await expect(page.getByText('2 options', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: 'Save changes' }).click()
    const warning = page.getByRole('dialog', { name: 'Remove options with votes?' })
    await expect(warning).toBeVisible()
    await expect(warning).toContainText('1 vote will be deleted along with the removed option.')
    await warning.getByRole('button', { name: 'Save anyway' }).click()

    await page.waitForURL(`**${pollPath}`)
    await expect(page.getByText('Poll updated.')).toBeVisible()
    await expect(page.getByTestId(/^option-header-/)).toHaveCount(2)

    // The guest's row lost exactly the vote on the removed day.
    await guestPage.reload()
    await waitForHydration(guestPage)
    await expect(guestRow.locator('span[data-answer="yes"]')).toHaveCount(1)
    await expect(guestPage.getByTestId(/^option-header-/)).toHaveCount(2)

    // --- a deadline that has already passed closes the poll ---
    await page.goto(`${pollPath}/edit`)
    await waitForHydration(page)
    await page.getByRole('switch', { name: 'Voting deadline' }).click()
    const yesterday = new Date(Date.now() - 86_400_000).toISOString().slice(0, 10)
    await page.locator('#creator-deadline-date').fill(yesterday)
    await page.getByRole('button', { name: 'Save changes' }).click()
    await page.waitForURL(`**${pollPath}`)
    await expect(page.getByText('Poll updated.')).toBeVisible()

    // The deadline job fires on the worker's next claim and the room event re-renders the owner's
    // page: status badge "Closed" (the countdown pill also reads "Closed" once expired).
    await expect(page.getByText('Closed', { exact: true }).first()).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('button', { name: 'Reopen voting' })).toBeVisible()

    // The guest sees the closed state and can no longer edit their answer.
    await expect(
      guestPage.getByText('Voting is closed. The answers are still here to read.'),
    ).toBeVisible({ timeout: 20_000 })
    await expect(guestPage.getByRole('button', { name: "Edit Edit Guest's answer" })).toHaveCount(0)
    await expect(guestPage.getByTestId('add-yourself-row')).toHaveCount(0)
  } finally {
    await guestContext.close()
  }
})
