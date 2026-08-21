import type { Page } from '@playwright/test'
import {
  expect,
  pickFirstEnabledDay,
  pickTwoCalendarDays,
  signIn,
  test,
  waitForHydration,
  waitForTurnstile,
} from './fixtures'

/**
 * Captures the images the README links to, from the real app against real seeded data.
 *
 * Excluded from the normal suite (see `testIgnore` in `playwright.config.ts`) because it writes
 * files into the working tree and asserts almost nothing. Run it deliberately:
 *
 *     bun run screenshots
 *
 * The PNGs are **not** produced by CI — a human runs this on a machine with Chromium and commits
 * the result, so the README always shows the UI as it actually looked at that commit.
 */

const OUT = 'docs/screenshots'

test.use({ viewport: { width: 1280, height: 800 } })

/** Waits for webfonts and lets entry animations settle, so runs are byte-stable-ish. */
async function settle(page: Page): Promise<void> {
  await page.evaluate(() => document.fonts.ready)
  await expect(page.locator('body')).toBeVisible()
}

async function shoot(page: Page, name: string): Promise<void> {
  await settle(page)
  await page.screenshot({
    path: `${OUT}/${name}.png`,
    animations: 'disabled',
    caret: 'hide',
  })
}

test('landing page, light and dark', { tag: '@screenshots' }, async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' })
  await page.goto('/')
  await waitForHydration(page)
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await shoot(page, 'landing-light')

  // The pre-paint theme script resolves `auto` against `prefers-color-scheme`, so emulating the
  // media query is enough — no need to poke localStorage.
  await page.emulateMedia({ colorScheme: 'dark' })
  await page.reload()
  await expect(page.locator('html')).toHaveClass(/dark/)
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await shoot(page, 'landing-dark')
})

test('poll page with votes', { tag: '@screenshots' }, async ({ page, browser, userWithPoll }) => {
  test.skip(!userWithPoll.pollId, 'seed route did not return a pollId')
  const pollId = userWithPoll.pollId!

  // Two guests vote differently so the grid has something to show and one option wins.
  for (const [name, answers] of [
    ['Ingrid', [1, 1]],
    ['Mikkel', [1, 2]],
    ['Sofie', [1, 3]],
  ] as const) {
    const context = await browser.newContext()
    const guest = await context.newPage()
    try {
      await guest.goto(`/p/${pollId}`)
      await waitForHydration(guest)
      await guest.getByTestId('add-yourself-row').getByLabel('Your name').fill(name)
      const cells = guest.locator('[data-testid="add-yourself-row"] button[data-answer]')
      await expect(cells).toHaveCount(2)
      // One tap = yes, two = if-need-be, three = no (VoteCell cycles through the answers).
      for (const [index, taps] of answers.entries()) {
        for (let tap = 0; tap < taps; tap++) await cells.nth(index).click()
      }
      await waitForTurnstile(guest)
      await guest.getByRole('button', { name: 'Save my answer' }).click()
      await expect(
        guest.locator('[data-testid^="participant-row-"]').filter({ hasText: name }),
      ).toBeVisible()
    } finally {
      await context.close()
    }
  }

  await signIn(page, userWithPoll)
  await page.goto(`/p/${pollId}`)
  await waitForHydration(page)
  await expect(page.getByTestId('vote-grid')).toBeVisible()
  await shoot(page, 'poll')
})

test('poll creator', { tag: '@screenshots' }, async ({ page, user }) => {
  await signIn(page, user)
  await page.goto('/new')
  await waitForHydration(page)
  await expect(page.getByTestId('creator-wizard')).toBeVisible()

  await page.locator('#creator-title').fill('Team offsite planning')
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await pickTwoCalendarDays(page)
  await expect(page.getByText('2 options')).toBeVisible()
  await shoot(page, 'creator')
})

test('dashboard', { tag: '@screenshots' }, async ({ page, userWithPoll }) => {
  await signIn(page, userWithPoll)
  await page.goto('/dashboard')
  await waitForHydration(page)
  await expect(page.locator('[data-testid="poll-card"]').first()).toBeVisible()
  await shoot(page, 'dashboard')
})

test('booking page', { tag: '@screenshots' }, async ({ page, userWithBookingPage }) => {
  test.skip(!userWithBookingPage.pageId, 'seed route did not return a pageId')
  const handle = userWithBookingPage.handle!
  const slug = userWithBookingPage.slug!

  await page.goto(`/book/${handle}/${slug}`)
  await waitForHydration(page)
  await expect(page.getByTestId('booking-page')).toBeVisible()

  // Pick the first open day so the shot shows a real list of slot chips, not the empty state.
  await pickFirstEnabledDay(page)
  await expect(page.getByTestId('slot-list')).toBeVisible()

  await shoot(page, 'booking')
})

test('sign-up sheet', { tag: '@screenshots' }, async ({ page, browser, userWithSignup }) => {
  test.skip(!userWithSignup.pollId, 'seed route did not return a pollId')
  const pollId = userWithSignup.pollId!

  // A guest claims a slot so the board has a claimant to show.
  const context = await browser.newContext()
  const guest = await context.newPage()
  try {
    await guest.goto(`/p/${pollId}`)
    await waitForHydration(guest)
    await expect(guest.getByTestId('slot-board')).toBeVisible()
    await guest
      .getByTestId('slot-card')
      .filter({ hasText: 'Slot 1' })
      .getByRole('button', { name: 'Claim a spot for Slot 1' })
      .click()

    const identityDialog = guest.getByRole('dialog', { name: "Who's taking the slot?" })
    await identityDialog.getByLabel('Your name').fill('Kari')
    await waitForTurnstile(guest)
    await identityDialog.getByRole('button', { name: 'Sign me up' }).click()
    await expect(identityDialog).toBeHidden()
  } finally {
    await context.close()
  }

  await signIn(page, userWithSignup)
  await page.goto(`/p/${pollId}`)
  await waitForHydration(page)
  await expect(page.getByTestId('slot-board')).toBeVisible()
  await shoot(page, 'signup')
})
