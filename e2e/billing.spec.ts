import { expect, signIn, test, waitForHydration } from './fixtures'

test.describe('billing', () => {
  test('free org shows the upgrade CTA on the settings page', async ({ page, user }) => {
    await signIn(page, user)
    await page.goto('/settings')
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: 'Billing' })).toBeVisible()
    await expect(page.getByText('Free plan')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Upgrade to Premium' })).toBeVisible()
  })

  test('seeded premium org shows the plan card and seat usage on the settings page', async ({
    page,
    userPremium,
  }) => {
    await signIn(page, userPremium)
    await page.goto('/settings')
    await waitForHydration(page)

    await expect(page.getByText('Premium plan')).toBeVisible()
    // Owner is the org's only member — 1 of 10 seats used.
    await expect(page.getByText('1 of 10 seats used')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Manage billing' })).toBeVisible()
  })

  test('a premium org can send an invite from the API, and the accept URL resolves', async ({
    page,
    userPremium,
  }) => {
    await signIn(page, userPremium)

    const inviteResponse = await page.request.post('/api/auth/organization/invite-member', {
      data: { email: `invitee-${Date.now()}@example.com`, role: 'member' },
    })
    expect(inviteResponse.ok()).toBe(true)
    const invitation = (await inviteResponse.json()) as { id: string }
    expect(invitation.id).toBeTruthy()

    await page.goto(`/accept-invitation/${invitation.id}`)
    await waitForHydration(page)

    await expect(page.getByRole('heading', { name: 'Join the organization' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Accept invitation' })).toBeVisible()
  })
})
