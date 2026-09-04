import { expect, signIn, test, waitForHydration } from './fixtures'

/**
 * Every other voter in the suite is an anonymous context holding an HMAC edit token. A signed-in
 * voter takes the other persistence path — the row is bound to the user id, so a SECOND device
 * (context) signed in as the same person must see it as "You" and be allowed to edit it, with no
 * token to hand over.
 */
test('a signed-in user votes, and a second signed-in context edits that same answer', async ({
  page,
  browser,
  user,
  userWithPoll,
}) => {
  const pollPath = `/p/${userWithPoll.pollId}`

  // Device 1: vote.
  await signIn(page, user)
  await page.goto(pollPath)
  await waitForHydration(page)
  await expect(page.getByTestId('vote-grid')).toBeVisible()

  const addRow = page.getByTestId('add-yourself-row')
  // The name is prefilled from the session (use-answer-draft.ts) — proves the profile name is used.
  await expect(addRow.getByRole('textbox', { name: 'Your name' })).toHaveValue(user.name)
  const cells = addRow.locator('button[data-answer]')
  await expect(cells).toHaveCount(2)
  await cells.nth(0).click() // yes
  await cells.nth(1).click() // yes
  await page.getByRole('button', { name: 'Save my answer' }).click()

  const rows = page.locator('[data-testid^="participant-row-"]')
  const myRow = rows.filter({ hasText: 'You' })
  await expect(myRow).toHaveCount(1)
  await expect(myRow).toContainText(user.name)
  await expect(myRow.locator('span[data-answer="yes"]')).toHaveCount(2)
  // Having answered, the add-yourself row is gone — a signed-in user gets exactly one row.
  await expect(addRow).toHaveCount(0)

  // Device 2: same person, fresh context, no edit token anywhere.
  const second = await browser.newContext()
  const secondPage = await second.newPage()
  try {
    await signIn(secondPage, user)
    await secondPage.goto(pollPath)
    await waitForHydration(secondPage)

    const theirRow = secondPage.locator('[data-testid^="participant-row-"]').filter({ hasText: 'You' })
    await expect(theirRow).toHaveCount(1)
    await theirRow.getByRole('button', { name: `Edit ${user.name}'s answer` }).click()

    const editCells = secondPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await expect(editCells).toHaveCount(2)
    await editCells.nth(0).click() // yes -> if need be
    await secondPage.getByRole('button', { name: 'Update answer' }).click()

    await expect(theirRow.locator('span[data-answer="ifneedbe"]')).toHaveCount(1)
    await expect(theirRow.locator('span[data-answer="yes"]')).toHaveCount(1)
  } finally {
    await second.close()
  }

  // Device 1 sees the edit live — same row, no duplicate.
  await expect(myRow.locator('span[data-answer="ifneedbe"]')).toHaveCount(1, { timeout: 10_000 })
  await expect(rows).toHaveCount(1)
})
