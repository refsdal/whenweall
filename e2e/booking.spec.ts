import { expect, signIn, test, waitForTurnstile } from './fixtures'

/**
 * Picks the first enabled day on the public booking page's month picker (`MonthPicker`, built on
 * the same `Calendar` primitive `pickTwoCalendarDays` drives for the poll creator), paging
 * forward a month at a time if the visible month has none yet. The seeded page (weekday
 * 09:00–17:00 Europe/Oslo, `min_notice_min: 0`) always has an enabled day within a couple of
 * months, so this never depends on which day of the month the suite happens to run on.
 */
async function pickFirstEnabledDay(page: import('@playwright/test').Page): Promise<void> {
  const calendar = page.locator('[data-slot="calendar"]')
  await expect(calendar).toBeVisible()

  const enabledDays = calendar.locator('button[data-day]:not([disabled])')
  for (let guard = 0; guard < 6 && (await enabledDays.count()) < 1; guard++) {
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
  }
  await expect(async () => {
    expect(await enabledDays.count()).toBeGreaterThanOrEqual(1)
  }).toPass({ timeout: 5_000 })

  await enabledDays.first().click()
}

test('a visitor books the first open slot, the owner sees it, and cancelling frees it live', async ({
  page,
  browser,
  request,
  userWithBookingPage,
}) => {
  test.skip(!userWithBookingPage.pageId, 'seed route did not return a pageId')
  const pageId = userWithBookingPage.pageId!
  const handle = userWithBookingPage.handle!
  const slug = userWithBookingPage.slug!
  const bookPath = `/book/${handle}/${slug}`

  // Context A: the visitor who is about to book.
  const visitorContext = await browser.newContext()
  const visitorPage = await visitorContext.newPage()
  // Context B: a second visitor, watching the same day live for the whole test.
  const watcherContext = await browser.newContext()
  const watcherPage = await watcherContext.newPage()

  try {
    // --- both visitors land on the same day, before anything is booked ---
    await visitorPage.goto(bookPath)
    await expect(visitorPage.getByTestId('booking-page')).toBeVisible()
    await pickFirstEnabledDay(visitorPage)

    await watcherPage.goto(bookPath)
    await expect(watcherPage.getByTestId('booking-page')).toBeVisible()
    await pickFirstEnabledDay(watcherPage)

    const visitorSlotList = visitorPage.getByTestId('slot-list')
    await expect(visitorSlotList).toBeVisible()
    const firstSlot = visitorSlotList.locator('button').first()
    const slotLabel = (await firstSlot.textContent())!.trim()
    expect(slotLabel).not.toBe('')
    const slotByLabel = (locatorPage: typeof visitorPage) =>
      locatorPage.getByTestId('slot-list').getByRole('button', { name: new RegExp(slotLabel) })

    // The watcher's day/month is chosen the same deterministic way, so it shows the same slot.
    await expect(slotByLabel(watcherPage)).toBeVisible()

    // --- the visitor claims that slot ---
    await firstSlot.click()
    const dialog = visitorPage.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await visitorPage.locator('#booking-name').fill('Visitor One')
    await visitorPage.locator('#booking-email').fill('visitor-one@example.com')
    await waitForTurnstile(visitorPage)
    await visitorPage.getByRole('button', { name: 'Confirm booking' }).click()

    const confirmed = visitorPage.getByTestId('booking-confirmed')
    await expect(confirmed).toBeVisible()

    const manageLink = confirmed.getByRole('link', { name: 'Change or cancel' })
    const href = await manageLink.getAttribute('href')
    expect(href).toBeTruthy()
    const match = href!.match(/^\/booking\/([^?]+)\?t=(.+)$/)
    expect(match).not.toBeNull()
    const bookingId = match![1]!
    const manageToken = decodeURIComponent(match![2]!)

    // --- the .ics for the fresh booking is a real calendar file ---
    const ics = await request.get(
      `/booking/${bookingId}/calendar.ics?t=${encodeURIComponent(manageToken)}`,
    )
    expect(ics.status()).toBe(200)
    expect(ics.headers()['content-type']).toContain('text/calendar')

    // --- the watcher sees the booked slot chip disappear live, within 10s, without a reload ---
    await expect(slotByLabel(watcherPage)).toHaveCount(0, { timeout: 10_000 })

    // --- the owner sees the visitor's name on the bookings page ---
    await signIn(page, userWithBookingPage)
    await page.goto(`/bookings/${pageId}`)
    await expect(page.getByTestId('booking-page-view')).toBeVisible()
    await expect(page.getByText('Visitor One')).toBeVisible()

    // --- the visitor cancels via the manage link ---
    await visitorPage.goto(`/booking/${bookingId}?t=${encodeURIComponent(manageToken)}`)
    await expect(visitorPage.getByTestId('manage-booking')).toBeVisible()
    await visitorPage.getByRole('button', { name: 'Cancel booking' }).click()
    const confirmDialog = visitorPage.getByRole('dialog', { name: 'Cancel this booking?' })
    await expect(confirmDialog).toBeVisible()
    await confirmDialog.getByRole('button', { name: 'Cancel booking' }).click()
    await expect(visitorPage.getByText('This booking is cancelled')).toBeVisible()

    // --- the slot reappears live for the watcher, within 10s, without a reload ---
    await expect(slotByLabel(watcherPage)).toBeVisible({ timeout: 10_000 })
  } finally {
    await visitorContext.close()
    await watcherContext.close()
  }
})
