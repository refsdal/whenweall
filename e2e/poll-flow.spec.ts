import {
  expect,
  pickTwoCalendarDays,
  signIn,
  test,
  waitForHydration,
} from './fixtures'
import { waitForMail } from './mailpit'

test('create a poll, guest votes, edits their answer, owner finalizes, .ics downloads', async ({
  page,
  request,
  browser,
  user,
}) => {
  await signIn(page, user)

  // --- creator wizard: title -> two future days -> defaults -> create ---
  await page.goto('/new')
  await waitForHydration(page)
  await expect(page.getByTestId('creator-wizard')).toBeVisible()

  const title = `E2E poll ${Date.now()}`
  await page.locator('#creator-title').fill(title)
  await page.getByRole('button', { name: 'Next', exact: true }).click()

  await pickTwoCalendarDays(page)
  await expect(page.getByText('2 options')).toBeVisible()

  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await page.getByRole('button', { name: 'Create poll' }).click()

  await page.waitForURL(/\/p\/[^/?]+/)
  const pollUrl = new URL(page.url())
  const pollId = pollUrl.pathname.split('/').filter(Boolean).pop()
  expect(pollId).toBeTruthy()
  const baseUrl = `${pollUrl.origin}${pollUrl.pathname}`

  // --- share sheet auto-opens after creation ---
  const shareDialog = page.getByRole('dialog', { name: 'Share this poll' })
  await expect(shareDialog).toBeVisible()
  await expect(page.locator('#share-url')).toHaveValue(baseUrl)
  await page.keyboard.press('Escape')
  await expect(shareDialog).toBeHidden()

  // --- guest opens the poll in a fresh, unauthenticated context and votes ---
  const guestContext = await browser.newContext()
  const guestPage = await guestContext.newPage()
  try {
    await guestPage.goto(baseUrl)
    await waitForHydration(guestPage)
    await expect(guestPage.getByTestId('vote-grid')).toBeVisible()

    const guestName = `Guest ${Date.now()}`
    const guestEmail = `guest-${Date.now()}@example.com`
    await guestPage.getByTestId('add-yourself-row').getByLabel('Your name').fill(guestName)
    await guestPage.getByRole('textbox', { name: 'Email' }).fill(guestEmail)
    const guestCells = guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await expect(guestCells).toHaveCount(2)
    await guestCells.nth(0).click()
    await guestCells.nth(1).click()
    await guestPage.getByRole('button', { name: 'Save my answer' }).click()

    const yourRow = guestPage.locator('[data-testid^="participant-row-"]').filter({
      hasText: 'You',
    })
    await expect(yourRow).toBeVisible()
    await expect(yourRow).toContainText(guestName)
    await expect(yourRow.locator('span[data-answer="yes"]')).toHaveCount(2)

    // --- guest edits their own answer ---
    await yourRow.getByRole('button', { name: `Edit ${guestName}'s answer` }).click()
    const editCells = guestPage.locator('[data-testid="add-yourself-row"] button[data-answer]')
    await editCells.nth(0).click() // yes -> if need be
    await guestPage.getByRole('button', { name: 'Update answer' }).click()

    const updatedRow = guestPage.locator('[data-testid^="participant-row-"]').filter({
      hasText: 'You',
    })
    await expect(updatedRow.locator('span[data-answer="ifneedbe"]')).toHaveCount(1)

    // --- owner finalizes the poll ---
    await page.getByRole('button', { name: 'Pick the winner' }).click()
    const finalizeDialog = page.getByRole('dialog', { name: 'Pick the winning option' })
    await expect(finalizeDialog).toBeVisible()
    await finalizeDialog.getByRole('button', { name: 'Confirm the pick' }).click()
    await expect(finalizeDialog).toBeHidden()

    // Plan C: the response carries `sent` (unique recipients enqueued) and the toast prints it.
    // The guest left an address, so at least one person was notified — never "undefined".
    const toast = page.getByText(/^Decided\. \d+ people were notified\.$/)
    await expect(toast).toBeVisible()
    const notified = Number(/\d+/.exec((await toast.textContent()) ?? '')?.[0])
    expect(notified).toBeGreaterThanOrEqual(1)

    const ownerBanner = page.getByTestId('finalized-banner')
    await expect(ownerBanner).toBeVisible()

    // --- guest sees the same decision after reloading ---
    await guestPage.reload()
    await expect(guestPage.getByTestId('finalized-banner')).toBeVisible()

    // The guest's "decided" mail is really sent, with the poll's title in the subject.
    const decided = await waitForMail(request, guestEmail, { subject: /is decided$/ })
    expect(decided.Subject).toBe(`${title} is decided`)
    expect(decided.Text).toContain(guestName)
  } finally {
    await guestContext.close()
  }

  // --- the finalized poll's calendar feed is downloadable ---
  // Downloads live under the API surface, not the SPA route — web/src/api/polls.ts's
  // `pollCalendarICSUrl` builds this same path, not `/p/{id}/calendar.ics`.
  const ics = await request.get(`/api/v1/polls/${pollId}/calendar.ics`)
  expect(ics.status()).toBe(200)
  expect(ics.headers()['content-type']).toContain('text/calendar')
  expect(await ics.text()).toContain('BEGIN:VCALENDAR')
})
