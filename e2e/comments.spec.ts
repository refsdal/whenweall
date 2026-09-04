import { expect, signIn, test, waitForHydration } from './fixtures'

test('a guest comment appears live for the owner, and the owner deleting it drops it live for the guest', async ({
  page,
  browser,
  userWithPoll,
}) => {
  const pollPath = `/p/${userWithPoll.pollId}`

  // Context A: the owner, watching the poll.
  await signIn(page, userWithPoll)
  await page.goto(pollPath)
  await waitForHydration(page)
  const ownerComments = page.getByRole('region', { name: /^Comments/ })
  await expect(ownerComments).toBeVisible()
  await expect(ownerComments.getByText('No comments yet. Start the conversation.')).toBeVisible()

  // Context B: an anonymous guest.
  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(pollPath)
    await waitForHydration(guestPage)
    const guestComments = guestPage.getByRole('region', { name: /^Comments/ })
    await expect(guestComments).toBeVisible()

    const body = `Late start works for me ${Date.now()}`
    // Scoped to the region: the vote grid's "add yourself" row has a "Your name" input of its own.
    await guestComments.getByRole('textbox', { name: 'Your name' }).fill('Comment Guest')
    await guestComments.getByRole('textbox', { name: /^Comments/ }).fill(body)
    await guestComments.getByRole('button', { name: 'Post comment' }).click()

    await expect(guestPage.getByText('Comment posted.')).toBeVisible()
    await expect(guestComments.getByText(body)).toBeVisible()
    // A guest is not the owner and has no session, so no delete control on their own comment.
    await expect(
      guestComments.getByRole('button', { name: 'Delete comment from Comment Guest' }),
    ).toHaveCount(0)

    // --- the owner's tab picks it up live, without a reload ---
    const ownerCopy = ownerComments.getByText(body)
    await expect(ownerCopy).toBeVisible({ timeout: 10_000 })

    // --- the owner deletes it ---
    // The delete button sits at opacity 0 until the row is hovered (sm:opacity-0) — still "visible"
    // to Playwright, but hovering first mirrors what a person does and avoids a flaky click.
    await ownerCopy.hover()
    await ownerComments.getByRole('button', { name: 'Delete comment from Comment Guest' }).click()
    await expect(page.getByText('Comment deleted.')).toBeVisible()
    await expect(ownerCopy).toHaveCount(0)

    // --- and the guest's tab drops it live too ---
    await expect(guestComments.getByText(body)).toHaveCount(0, { timeout: 10_000 })
    await expect(guestComments.getByText('No comments yet. Start the conversation.')).toBeVisible()
  } finally {
    await guestContext.close()
  }
})
