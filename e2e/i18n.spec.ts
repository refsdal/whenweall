import { expect, test, waitForHydration } from './fixtures'

test('switching locale updates the page and persists across a reload', async ({ page }) => {
  await page.goto('/')
  await waitForHydration(page)

  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Find a time everyone can make.')

  const switcher = page.getByRole('navigation').getByRole('group', { name: 'Language' })
  await switcher.getByRole('button', { name: 'NO' }).click()

  // `setLocale` sets the `whenweall_locale` cookie and reloads the page.
  await expect(page.locator('html')).toHaveAttribute('lang', 'nb', { timeout: 10_000 })
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(
    'Finn et tidspunkt som passer for alle.',
  )

  await page.reload()

  await expect(page.locator('html')).toHaveAttribute('lang', 'nb')
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(
    'Finn et tidspunkt som passer for alle.',
  )
})
