import {
  expect,
  pickFirstEnabledDayNotToday,
  signIn,
  test,
  waitForHydration,
  waitForTurnstile,
} from './fixtures'

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
    await waitForHydration(visitorPage)
    await expect(visitorPage.getByTestId('booking-page')).toBeVisible()
    await pickFirstEnabledDayNotToday(visitorPage)

    await watcherPage.goto(bookPath)
    await waitForHydration(watcherPage)
    await expect(watcherPage.getByTestId('booking-page')).toBeVisible()
    await pickFirstEnabledDayNotToday(watcherPage)

    const visitorSlotList = visitorPage.getByTestId('slot-list')
    await expect(visitorSlotList).toBeVisible()
    const firstSlot = visitorSlotList.locator('button').first()
    const slotLabel = (await firstSlot.textContent())!.trim()
    expect(slotLabel).not.toBe('')
    // Anchor on the chip's start time: a slot's accessible name is "start – end", so an
    // unanchored /14:30/ would also match the 14:00–14:30 chip.
    const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const slotByLabel = (locatorPage: typeof visitorPage) =>
      locatorPage
        .getByTestId('slot-list')
        .getByRole('button', { name: new RegExp('^' + escapeRegExp(slotLabel)) })

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
    // Downloads live under the API surface, not the SPA route — web/src/api/bookings.ts's
    // `bookingCalendarICSUrl` builds this same path, mirroring polls' own
    // `pollCalendarICSUrl`/`/api/v1/polls/{id}/calendar.ics` (poll-flow.spec.ts).
    const ics = await request.get(
      `/api/v1/bookings/${bookingId}/calendar.ics?t=${encodeURIComponent(manageToken)}`,
    )
    expect(ics.status()).toBe(200)
    expect(ics.headers()['content-type']).toContain('text/calendar')
    expect(await ics.text()).toContain('BEGIN:VCALENDAR')

    // --- the watcher sees the booked slot chip disappear live, within 10s, without a reload ---
    await expect(slotByLabel(watcherPage)).toHaveCount(0, { timeout: 10_000 })

    // --- the owner sees the visitor's name on the bookings page ---
    await signIn(page, userWithBookingPage)
    await page.goto(`/bookings/${pageId}`)
    await waitForHydration(page)
    await expect(page.getByTestId('booking-page-view')).toBeVisible()
    await expect(page.getByText('Visitor One')).toBeVisible()

    // --- the visitor cancels via the manage link ---
    await visitorPage.goto(`/booking/${bookingId}?t=${encodeURIComponent(manageToken)}`)
    await waitForHydration(visitorPage)
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
