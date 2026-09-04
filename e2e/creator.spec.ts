import { expect, pickTwoCalendarDays, signIn, test, waitForHydration } from './fixtures'

// The creator defaults its time zone to the browser's; pin it so the assertions on rendered clock
// times below are the same on every machine (and so the Asia/Tokyo switch has a known offset).
test.use({ timezoneId: 'Europe/Oslo' })

async function openWizard(page: import('@playwright/test').Page, title: string): Promise<void> {
  await page.goto('/new')
  await waitForHydration(page)
  await expect(page.getByTestId('creator-wizard')).toBeVisible()
  await page.locator('#creator-title').fill(title)
}

async function createAndDismissShare(page: import('@playwright/test').Page): Promise<string> {
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Create poll' }).click()
  await page.waitForURL(/\/p\/[^/?]+/)
  const shareDialog = page.getByRole('dialog', { name: 'Share this poll' })
  await expect(shareDialog).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(shareDialog).toBeHidden()
  const url = new URL(page.url())
  return `${url.origin}${url.pathname}`
}

test('a choice poll ("Pizza / Sushi / Thai") is created through the wizard and a guest votes on it', async ({
  page,
  browser,
  user,
}) => {
  await signIn(page, user)
  await openWizard(page, `Dinner ${Date.now()}`)

  // Type card: accessible name is "Anything else" + its description, hence the anchored regex.
  await page.getByRole('button', { name: /^Anything else/ }).click()
  await expect(page.getByRole('button', { name: /^Anything else/ })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await page.getByRole('textbox', { name: 'Option 1' }).fill('Pizza')
  await page.getByRole('textbox', { name: 'Option 2' }).fill('Sushi')
  await page.getByRole('button', { name: 'Add option' }).click()
  await page.getByRole('textbox', { name: 'Option 3' }).fill('Thai')
  await expect(page.getByText('3 options')).toBeVisible()

  const pollUrl = await createAndDismissShare(page)

  for (const label of ['Pizza', 'Sushi', 'Thai']) {
    await expect(page.getByRole('columnheader', { name: label })).toBeVisible()
  }
  // A choice poll has no clock times, so no time-zone switch is offered.
  await expect(page.getByText(/^Times shown in /)).toHaveCount(0)

  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(pollUrl)
    await waitForHydration(guestPage)
    await expect(guestPage.getByTestId('vote-grid')).toBeVisible()

    const guestName = `Hungry Guest ${Date.now()}`
    await guestPage.getByTestId('add-yourself-row').getByLabel('Your name').fill(guestName)
    const cells = guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await expect(cells).toHaveCount(3)
    await cells.nth(0).click() // Pizza: yes
    await cells.nth(2).click() // Thai: yes
    await cells.nth(2).click() // Thai: if need be
    await guestPage.getByRole('button', { name: 'Save my answer' }).click()

    const guestRow = guestPage.locator('[data-testid^="participant-row-"]').filter({ hasText: 'You' })
    await expect(guestRow).toContainText(guestName)
    await expect(guestRow.locator('span[data-answer="yes"]')).toHaveCount(1)
    await expect(guestRow.locator('span[data-answer="ifneedbe"]')).toHaveCount(1)

    // The owner's tab gets the row live.
    await expect(
      page.locator('[data-testid^="participant-row-"]').filter({ hasText: guestName }),
    ).toBeVisible({ timeout: 10_000 })
  } finally {
    await guestContext.close()
  }
})

test('a dates poll with one time slot applied to all days shows clock times, and the time-zone switch re-renders them', async ({
  page,
  user,
}) => {
  await signIn(page, user)
  await openWizard(page, `Stand-up ${Date.now()}`)
  // "Dates & times" is the default type — straight on.
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await pickTwoCalendarDays(page)
  await expect(page.getByText('2 options')).toBeVisible()

  // The selected-days list: each <li> carries a "Remove {day}" button, so the first such <li> is
  // the first day's card (its nested slot list has no such button yet).
  const firstDay = page
    .locator('li')
    .filter({ has: page.getByRole('button', { name: /^Remove / }) })
    .first()
  await firstDay.getByRole('button', { name: '10:00', exact: true }).click()
  // Default duration is 1 h, so the preview and the chip read 10:00 – 11:00.
  await firstDay.getByRole('button', { name: 'Add time' }).click()
  await expect(firstDay.getByText('10:00 – 11:00')).toBeVisible()

  await firstDay.getByRole('button', { name: 'Apply to all days' }).click()
  await expect(page.getByText('Times copied to every selected day.')).toBeVisible()
  // A day with a slot stops being an all-day option: one slot per day, two options in total.
  await expect(page.getByText('10:00 – 11:00')).toHaveCount(2)
  await expect(page.getByText('2 options')).toBeVisible()

  await createAndDismissShare(page)

  const headers = page.getByTestId(/^option-header-/)
  await expect(headers).toHaveCount(2)
  await expect(headers.first()).toContainText('10:00')
  await expect(headers.first()).toContainText('– 11:00')
  await expect(page.getByText('Times shown in Europe/Oslo')).toBeVisible()

  // Switch the viewer's zone: Oslo is UTC+1 (winter) or UTC+2 (summer), Tokyo UTC+9, so 10:00
  // Oslo renders as 18:00 or 17:00 in Tokyo depending on the date the suite runs.
  // `exact: true` disambiguates from the header's theme toggle, whose aria-label
  // ("Change theme (currently System)") otherwise also matches this substring search.
  await page.getByRole('button', { name: 'change', exact: true }).click()
  await page.locator('#poll-timezone').selectOption('Asia/Tokyo')
  await expect(page.getByText('Times shown in Asia/Tokyo')).toBeVisible()
  await expect(headers.first()).not.toContainText('10:00')
  await expect(headers.first()).toContainText(/1[78]:00/)
  await expect(headers.first()).toContainText(/– 1[89]:00/)

  // Back to the organiser's zone via the reset link in the same popover.
  await page.getByRole('button', { name: "Use the organiser's zone (Europe/Oslo)" }).click()
  await expect(headers.first()).toContainText('10:00')
})
