import { expect, pickFirstEnabledDayNotToday, signIn, test, waitForHydration } from './fixtures'
import { extractLink, waitForMail } from './mailpit'

// The editor takes the browser's zone for a new page; pinning it makes the slot arithmetic below
// (13:00–15:00 in 30-minute steps = 4 chips) hold on every machine. `browser.newContext()` does
// NOT inherit `test.use` options, so the visitor context sets the same zone explicitly.
test.use({ timezoneId: 'Europe/Oslo' })

/** One unfolded property's value out of a .ics body — every property this file's calendar ever
 * emits (UID, DTSTART, SUMMARY) is short enough to never hit RFC 5545 line folding, so a plain
 * line match is enough; `\r?` absorbs the CRLF line endings `internal/ics.BuildCalendar` joins on. */
function icsProperty(icsText: string, name: string): string {
  const match = icsText.match(new RegExp(`^${name}:(.*?)\\r?$`, 'm'))
  if (!match) throw new Error(`no ${name} property in .ics:\n${icsText}`)
  return match[1]!
}

/**
 * DTSTART ("YYYYMMDDTHHMMSSZ", always UTC — `internal/ics.FormatUTCBasic`) read back as the
 * "HH:MM" a viewer in `timeZone` would see it as. Comparing the wall-clock time this way, rather
 * than a fixed UTC string, survives the DST boundary: Oslo's offset from UTC differs by season,
 * but re-deriving the local hour from the UTC instant through the same zone the booking was made
 * in always gives back the time that was actually picked, on any date the suite happens to run.
 */
function icsStartTimeIn(icsText: string, timeZone: string): string {
  const raw = icsProperty(icsText, 'DTSTART')
  const iso = raw.replace(/^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z$/, '$1-$2-$3T$4:$5:$6Z')
  return new Intl.DateTimeFormat('en-GB', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).format(new Date(iso))
}

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
    const cardIcsMatch = cardIcs!.match(/^\/api\/v1\/bookings\/([^/?]+)\/calendar\.ics\?t=.+/)
    expect(cardIcsMatch).not.toBeNull()
    const bookingId = cardIcsMatch![1]!
    const ics = await request.get(cardIcs!)
    expect(ics.status()).toBe(200)
    expect(ics.headers()['content-type']).toContain('text/calendar')
    const icsText = await ics.text()
    expect(icsText).toContain('BEGIN:VCALENDAR')
    // Tied to THIS booking (UID embeds its id — internal/bookings/ics.go) and to the exact slot
    // just booked, not merely "some" valid calendar file: a feed that served a stale or unrelated
    // booking's event would fail one of the next two lines even though it passed every check above.
    expect(icsProperty(icsText, 'UID')).toBe(`${bookingId}@whenweall`)
    expect(icsProperty(icsText, 'SUMMARY')).toBe('Office hours')
    expect(icsStartTimeIn(icsText, 'Europe/Oslo')).toBe('13:00')

    // --- the confirmation mail carries the manage link ---
    const confirmation = await waitForMail(request, visitorEmail, { subject: /^Confirmed: Office hours/ })
    expect(confirmation.Text).toContain('Booker One')
    // "Wed 9 Sep, 13:00–13:30" (mailer.FormatTimeRange, internal/mailer/format.go): always
    // rendered 24-hour with no AM/PM regardless of locale, so this is a stable, locale- and
    // DST-independent substring tying the mail to the exact slot just booked.
    expect(confirmation.Text).toContain('13:00–13:30')
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
    const manageIcsMatch = manageIcs!.match(/^\/api\/v1\/bookings\/([^/?]+)\/calendar\.ics\?t=.+/)
    expect(manageIcsMatch).not.toBeNull()
    expect(manageIcsMatch![1]).toBe(bookingId) // same booking, moved — not a new one
    const movedIcs = await request.get(manageIcs!)
    expect(movedIcs.status()).toBe(200)
    expect(movedIcs.headers()['content-type']).toContain('text/calendar')
    const movedIcsText = await movedIcs.text()
    expect(movedIcsText).toContain('BEGIN:VCALENDAR')
    // The feed must have actually re-read the rescheduled time, not served a cached/stale event:
    // same UID as before, but DTSTART now reflects 13:30, the slot just moved to — a spec that
    // repeated the pre-reschedule assertions verbatim here would pass even if the feed never
    // updated at all.
    expect(icsProperty(movedIcsText, 'UID')).toBe(`${bookingId}@whenweall`)
    expect(icsStartTimeIn(movedIcsText, 'Europe/Oslo')).toBe('13:30')

    // The reschedule mail must carry the NEW time, not just the right subject line. Its body reads
    // "...has moved from {previousWhen} to {when}" (email_booking_rescheduled_body,
    // internal/mailer/messages.go): previousWhen has no recorded end so it renders as a bare point
    // in time ("Wed 9 Sep, 13:00" — bookingWhenText's `end == nil` branch), while `when` is the
    // real "13:30–14:00" range — a mail that echoed the vacated slot for `when` too, or dropped the
    // new time, would fail one of these even though the subject alone already passed.
    const rescheduled = await waitForMail(request, visitorEmail, {
      subject: /^Rescheduled: Office hours/,
    })
    expect(rescheduled.Text).toContain('13:00') // previousWhen: the vacated slot's start
    expect(rescheduled.Text).toContain('13:30–14:00') // when: the range it moved to
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
