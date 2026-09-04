import { expect, pickFirstEnabledDayNotToday, signIn, test, waitForHydration } from './fixtures'
import { extractLink, waitForMail } from './mailpit'

// The editor takes the browser's zone for a new page; pinning it makes the slot arithmetic below
// (13:00–15:00 in 30-minute steps = 4 chips) hold on every machine. `browser.newContext()` does
// NOT inherit `test.use` options, so the visitor context sets the same zone explicitly.
test.use({ timezoneId: 'Europe/Oslo' })

test('an organiser sets a handle and creates a page; a visitor books, reschedules, downloads the .ics and gets the mails', async ({
  page,
  browser,
  request,
  user,
}) => {
  await signIn(page, user)

  // --- handle first: booking links start with it ---
  await page.goto('/settings')
  await waitForHydration(page)
  const handle = `e2e-${Date.now().toString(36)}`
  await page.locator('#settings-handle').fill(handle)
  await page.getByRole('button', { name: 'Save handle' }).click()
  await expect(page.getByText('Handle saved')).toBeVisible()
  await expect(page.getByText(`localhost:3100/book/${handle}`)).toBeVisible()

  // --- the page: one weekday window, Wednesday 13:00–15:00 ---
  await page.goto('/bookings/new')
  await waitForHydration(page)
  await expect(page.getByTestId('page-editor')).toBeVisible()
  await page.locator('#booking-title').fill('Office hours')
  await expect(page.locator('#booking-slug')).toHaveValue('office-hours')
  await expect(page.getByText(`localhost:3100/book/${handle}/office-hours`)).toBeVisible()

  for (const day of ['Monday', 'Tuesday', 'Thursday', 'Friday']) {
    await page.getByRole('switch', { name: `${day} availability` }).click()
    await expect(page.getByRole('switch', { name: `${day} availability` })).not.toBeChecked()
  }
  await expect(page.getByRole('switch', { name: 'Wednesday availability' })).toBeChecked()
  await page.getByLabel('Wednesday start time').fill('13:00')
  await page.getByLabel('Wednesday end time').fill('15:00')

  await page.getByRole('button', { name: 'Create page' }).click()
  await expect(page.getByText('Booking page created')).toBeVisible()
  await page.waitForURL(/\/bookings\/[^/?]+$/)
  await expect(page.getByTestId('booking-page-view')).toBeVisible()
  const pageId = new URL(page.url()).pathname.split('/').filter(Boolean).pop()!

  // --- a visitor books ---
  const visitorEmail = `booker-${Date.now()}@example.com`
  const visitorContext = await browser.newContext({ timezoneId: 'Europe/Oslo' })
  const visitorPage = await visitorContext.newPage()
  try {
    await visitorPage.goto(`/book/${handle}/office-hours`)
    await waitForHydration(visitorPage)
    await expect(visitorPage.getByTestId('booking-page')).toBeVisible()
    await expect(visitorPage.getByRole('heading', { name: 'Office hours' })).toBeVisible()

    // Only Wednesdays are enabled, so this lands on a Wednesday that is not today.
    await pickFirstEnabledDayNotToday(visitorPage)
    const slotList = visitorPage.getByTestId('slot-list')
    await expect(slotList).toBeVisible()
    await expect(slotList.getByRole('button')).toHaveCount(4)
    await expect(slotList.getByRole('button', { name: '13:00 to 13:30' })).toBeVisible()

    await slotList.getByRole('button', { name: '13:00 to 13:30' }).click()
    await expect(visitorPage.getByRole('dialog')).toBeVisible()
    await visitorPage.locator('#booking-name').fill('Booker One')
    await visitorPage.locator('#booking-email').fill(visitorEmail)
    await visitorPage.getByRole('button', { name: 'Confirm booking' }).click()

    const confirmed = visitorPage.getByTestId('booking-confirmed')
    await expect(confirmed).toBeVisible()
    await expect(confirmed).toContainText(visitorEmail)

    // Plan D: the card's "Add to calendar" points at the API .ics, not the dead SPA path.
    const cardIcs = await confirmed.getByRole('link', { name: 'Add to calendar' }).getAttribute('href')
    expect(cardIcs).toMatch(/^\/api\/v1\/bookings\/[^/?]+\/calendar\.ics\?t=.+/)
    const ics = await request.get(cardIcs!)
    expect(ics.status()).toBe(200)
    expect(ics.headers()['content-type']).toContain('text/calendar')
    expect(await ics.text()).toContain('BEGIN:VCALENDAR')

    // --- the confirmation mail carries the manage link ---
    const confirmation = await waitForMail(request, visitorEmail, { subject: /^Confirmed: Office hours/ })
    expect(confirmation.Text).toContain('Booker One')
    const manageLink = extractLink(confirmation, '/booking/')
    expect(manageLink.searchParams.get('t')).toBeTruthy()

    // --- reschedule from the manage page ---
    await visitorPage.goto(manageLink.pathname + manageLink.search)
    await waitForHydration(visitorPage)
    await expect(visitorPage.getByTestId('manage-booking')).toBeVisible()
    await expect(visitorPage.getByText('Confirmed', { exact: true })).toBeVisible()

    await visitorPage.getByRole('button', { name: 'Reschedule' }).click()
    const dialog = visitorPage.getByRole('dialog', { name: 'Pick a new time' })
    await expect(dialog).toBeVisible()
    await pickFirstEnabledDayNotToday(dialog)
    const dialogSlots = dialog.getByTestId('slot-list')
    await expect(dialogSlots).toBeVisible()
    // The booked 13:00 chip is gone from this day; 13:30 is the first free one now.
    await dialogSlots.getByRole('button', { name: '13:30 to 14:00' }).click()
    await dialog.getByRole('button', { name: 'Move booking' }).click()
    await expect(visitorPage.getByText('Booking moved')).toBeVisible()
    await expect(dialog).toBeHidden()
    await expect(visitorPage.getByTestId('manage-booking')).toContainText('13:30')

    // --- the manage page's .ics is a real calendar file for the moved booking ---
    const manageIcs = await visitorPage
      .getByRole('link', { name: 'Add to calendar' })
      .getAttribute('href')
    expect(manageIcs).toMatch(/^\/api\/v1\/bookings\/[^/?]+\/calendar\.ics\?t=.+/)
    const movedIcs = await request.get(manageIcs!)
    expect(movedIcs.status()).toBe(200)
    expect(movedIcs.headers()['content-type']).toContain('text/calendar')
    expect(await movedIcs.text()).toContain('BEGIN:VCALENDAR')

    await waitForMail(request, visitorEmail, { subject: /^Rescheduled: Office hours/ })
  } finally {
    await visitorContext.close()
  }

  // --- the organiser sees the booking on the page's own view, and got the organiser mail ---
  await page.goto(`/bookings/${pageId}`)
  await waitForHydration(page)
  await expect(page.getByTestId('booking-page-view')).toBeVisible()
  await expect(page.getByText('Booker One')).toBeVisible()
  await waitForMail(request, user.email, { subject: /^New booking: Office hours/ })
})
