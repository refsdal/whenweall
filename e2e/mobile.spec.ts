import { expect, test, waitForHydration } from './fixtures'

/**
 * The poll page on a phone, where the grid is replaced by a list of dates.
 *
 * Runs at 390×844 (an iPhone 14, the most common handset size) — below the `sm` breakpoint, so
 * `VoteGrid` is display:none and `VoteList` is what a visitor actually touches. Playwright's role
 * and visibility engines ignore the hidden layout, which is exactly the guarantee worth pinning
 * down: both layouts are mounted, and only one of them may be reachable.
 */
test.describe('poll on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  test('a guest answers from the date list and sees their answer counted', async ({
    page,
    userWithPoll,
  }) => {
    const { pollId } = userWithPoll

    await page.goto(`/p/${pollId}`)
    await waitForHydration(page)

    // The list is the phone layout; the grid is mounted but must be unreachable.
    await expect(page.getByTestId('vote-list')).toBeVisible()
    await expect(page.getByTestId('vote-grid')).toBeHidden()

    const rows = page.getByTestId(/^vote-row-/)
    await expect(rows).toHaveCount(2)

    // No sideways scrolling: the whole page fits the viewport's width.
    const overflows = await page.evaluate(
      () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
    )
    expect(overflows).toBe(false)

    // Every answer control clears the 44px minimum touch target.
    const answerBox = await page.getByTestId('your-answer').first().boundingBox()
    expect(answerBox?.width ?? 0).toBeGreaterThanOrEqual(44)
    expect(answerBox?.height ?? 0).toBeGreaterThanOrEqual(44)

    // One tap per date: yes on the first, yes then if-need-be on the second.
    const answers = page.getByTestId('your-answer')
    await answers.nth(0).click()
    await expect(answers.nth(0)).toHaveAttribute('data-answer', 'yes')
    await answers.nth(1).click()
    await answers.nth(1).click()
    await expect(answers.nth(1)).toHaveAttribute('data-answer', 'ifneedbe')

    // The tally moves with the unsaved answer, counting the visitor as a person-to-be.
    await expect(rows.first()).toHaveAttribute('data-yes', '1')
    await expect(rows.first()).toHaveAttribute('data-total', '1')

    const saveBar = page.getByTestId('answer-save-bar')
    await expect(saveBar).toBeVisible()
    await expect(saveBar).toContainText('2 of 2 answered')

    // By id, not by label: the grid's own name input is still in the DOM (display:none), and
    // `getByLabel` does not filter on visibility the way the role engine does.
    const guestName = `Phone guest ${Date.now()}`
    await page.locator('#poll-your-name').fill(guestName)
    await saveBar.getByRole('button', { name: /save my answer/i }).click()

    // Saved: the visitor is now a row on the sheet, and the list says so.
    await expect(page.getByTestId('answer-save-bar')).toBeHidden()
    await expect(rows.first()).toHaveAttribute('data-yes', '1')

    // Opening a date reveals who said what, including the answer just saved.
    await rows
      .first()
      .getByRole('button', { name: /who said what/i })
      .click()
    await expect(rows.first().getByRole('list')).toContainText(guestName)

    // And it can be changed again from the same list.
    await page.getByRole('button', { name: /edit my answers/i }).click()
    await expect(page.getByTestId('your-answer').first()).toHaveAttribute('data-answer', 'yes')
  })
})
