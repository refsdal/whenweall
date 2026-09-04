import { expect, signIn, test, waitForHydration } from './fixtures'

test('lists a created poll, duplicates it as a "(copy)", and deletes it', async ({
  page,
  userWithPoll,
}) => {
  await signIn(page, userWithPoll)
  await page.goto('/dashboard')
  await waitForHydration(page)

  // `exact: true` on the title text, not `hasText` on the card: a substring match would also
  // accept "Seeded test poll (copy)", making the original/copy assertions below indistinguishable.
  const cardTitled = (title: string) =>
    page.locator('[data-testid="poll-card"]').filter({ has: page.getByText(title, { exact: true }) })

  const original = cardTitled('Seeded test poll')
  await expect(original).toHaveCount(1)

  await original.getByRole('button', { name: 'Duplicate' }).click()
  // PollCard's duplicate action navigates straight to the new poll.
  await page.waitForURL(/\/p\/[^/?]+$/)
  await expect(page.getByRole('heading', { name: 'Seeded test poll (copy)' })).toBeVisible()

  await page.goto('/dashboard')
  await waitForHydration(page)
  const copy = cardTitled('Seeded test poll (copy)')
  await expect(copy).toHaveCount(1)
  await expect(original).toHaveCount(1)

  await copy.getByRole('button', { name: 'Delete poll' }).click()
  await page.getByRole('button', { name: 'Delete', exact: true }).click()

  await expect(page.getByText('Poll deleted.')).toBeVisible()
  await expect(copy).toHaveCount(0)
  await expect(original).toHaveCount(1)
})
