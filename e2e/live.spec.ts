import { expect, signIn, test, waitForTurnstile } from './fixtures'

test('a guest vote appears live in another tab, and the presence pill shows two viewers', async ({
  page,
  browser,
  userWithPoll,
}) => {
  test.skip(!userWithPoll.pollId, 'seed route did not return a pollId')
  const pollId = userWithPoll.pollId!

  // Context A: the poll owner, watching the page.
  await signIn(page, userWithPoll)
  await page.goto(`/p/${pollId}`)
  await expect(page.getByTestId('vote-grid')).toBeVisible()

  // Context B: a guest, in a fresh incognito context, opens the same poll.
  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(`/p/${pollId}`)
    await expect(guestPage.getByTestId('vote-grid')).toBeVisible()

    // Once both sockets are connected, the presence pill on A reports two viewers.
    const presence = page.getByTestId('presence-pill')
    await expect(presence).toBeVisible({ timeout: 10_000 })
    await expect(presence).toHaveAttribute('data-count', '2', { timeout: 10_000 })

    // Guest votes...
    const guestName = `Live Guest ${Date.now()}`
    await guestPage.getByTestId('add-yourself-row').getByLabel('Your name').fill(guestName)
    await guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]').first().click()
    await waitForTurnstile(guestPage)
    await guestPage.getByRole('button', { name: 'Save my answer' }).click()

    // ...and A sees the new row appear on its own, without a reload, within 10s.
    const newRow = page.locator('[data-testid^="participant-row-"]').filter({
      hasText: guestName,
    })
    await expect(newRow).toBeVisible({ timeout: 10_000 })
  } finally {
    await guestContext.close()
  }
})
