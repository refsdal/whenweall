import { expect, test, waitForHydration } from './fixtures'

test('home page renders', async ({ page }) => {
  await page.goto('/')
  await waitForHydration(page)
  await expect(page.locator('h1')).toContainText('Find a time everyone can make.')
})
