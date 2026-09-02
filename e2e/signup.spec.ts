import { expect, signIn, test, waitForHydration, waitForTurnstile } from './fixtures'

/**
 * Claims a slot for a fresh guest page: opens the identity sheet on first claim, fills the name,
 * waits out the Turnstile test widget, and submits. Mirrors the flow a real guest goes through
 * the first time they press "Claim spot" on a sign-up sheet — see `IdentitySheet.tsx`.
 */
async function claimSlotAsNewGuest(
  page: import('@playwright/test').Page,
  slotLabel: string,
  name: string,
): Promise<void> {
  const card = page.getByTestId('slot-card').filter({ hasText: slotLabel })
  await card.getByRole('button', { name: `Claim a spot for ${slotLabel}` }).click()

  const identityDialog = page.getByRole('dialog', { name: "Who's taking the slot?" })
  await expect(identityDialog).toBeVisible()
  await identityDialog.getByLabel('Your name').fill(name)
  await waitForTurnstile(page)
  await identityDialog.getByRole('button', { name: 'Sign me up' }).click()
  await expect(identityDialog).toBeHidden()
}

test('guest claims a slot, a second guest fills it and claims the other, owner downloads the roster', async ({
  page,
  request,
  browser,
  userWithSignup,
}) => {
  test.skip(!userWithSignup.pollId, 'seed route did not return a pollId')
  const pollId = userWithSignup.pollId!
  const pollPath = `/p/${pollId}`
  // The roster download lives under the API surface, not the SPA route — web/src/api/polls.ts's
  // `pollRosterCSVUrl` builds this same path, not `/p/{id}/roster.csv`.
  const rosterPath = `/api/v1/polls/${pollId}/roster.csv`

  // Context A: guest A, a fresh unauthenticated browser context.
  const contextA = await browser.newContext()
  const pageA = await contextA.newPage()
  // Context B: guest B, another fresh unauthenticated context.
  const contextB = await browser.newContext()
  const pageB = await contextB.newPage()

  try {
    // --- guest A opens the sheet and claims Slot 1 (capacity 1) ---
    await pageA.goto(pollPath)
    await waitForHydration(pageA)
    await expect(pageA.getByTestId('slot-board')).toBeVisible()

    await claimSlotAsNewGuest(pageA, 'Slot 1', 'Guest A')

    const slot1A = pageA.getByTestId('slot-card').filter({ hasText: 'Slot 1' })
    await expect(slot1A).toHaveAttribute('data-full', 'true')
    await expect(slot1A).toContainText('Guest A')

    // --- guest B, in a fresh context, sees Slot 1 full and claims Slot 2 (unlimited) instead ---
    await pageB.goto(pollPath)
    await waitForHydration(pageB)
    await expect(pageB.getByTestId('slot-board')).toBeVisible()

    const slot1B = pageB.getByTestId('slot-card').filter({ hasText: 'Slot 1' })
    await expect(slot1B).toHaveAttribute('data-full', 'true')
    await expect(slot1B.getByRole('button', { name: 'Claim a spot for Slot 1' })).toBeDisabled()

    await claimSlotAsNewGuest(pageB, 'Slot 2', 'Guest B')

    const slot2B = pageB.getByTestId('slot-card').filter({ hasText: 'Slot 2' })
    await expect(slot2B).toContainText('Guest B')

    // --- guest A's page reflects guest B's claim live, without a reload, within 10s ---
    const slot2A = pageA.getByTestId('slot-card').filter({ hasText: 'Slot 2' })
    await expect(slot2A).toContainText('Guest B', { timeout: 10_000 })
  } finally {
    await contextA.close()
    await contextB.close()
  }

  // --- the owner downloads the roster: both names, as CSV, with their own session cookies ---
  await signIn(page, userWithSignup)
  const roster = await page.context().request.get(rosterPath)
  expect(roster.status()).toBe(200)
  expect(roster.headers()['content-type']).toContain('text/csv')
  const csv = await roster.text()
  expect(csv).toContain('Guest A')
  expect(csv).toContain('Guest B')

  // --- anonymous access to the same roster is rejected (401: no session at all) ---
  const anonymousRoster = await request.get(rosterPath)
  expect(anonymousRoster.status()).toBe(401)
})
