import { expect, signIn, test } from './fixtures'

test('lists a created poll, duplicates it as a "(copy)", and deletes it', async ({
  page,
  userWithPoll,
}) => {
  await signIn(page, userWithPoll)
  await page.goto('/dashboard')

  const original = page.locator('[data-testid="poll-card"]', { hasText: 'Seeded test poll' })
  await expect(original).toBeVisible()

  await original.getByRole('button', { name: 'Duplicate' }).click()
  // PollCard's duplicate action navigates straight to the new poll.
  await page.waitForURL(/\/p\/[^/?]+$/)

  await page.goto('/dashboard')
  const copy = page.locator('[data-testid="poll-card"]', {
    hasText: 'Seeded test poll (copy)',
  })
  await expect(copy).toBeVisible()

  await copy.getByRole('button', { name: 'Delete poll' }).click()
  await page.getByRole('button', { name: 'Delete', exact: true }).click()

  await expect(copy).toBeHidden()
  await expect(original).toBeVisible()
})
