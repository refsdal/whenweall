import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test'

/** What `POST /api/test/seed` hands back for a freshly created, already-verified user. */
export type SeededUser = {
  email: string
  password: string
  name: string
  pollId?: string
  pageId?: string
  handle?: string
  slug?: string
}

async function seed(
  request: APIRequestContext,
  opts: { name?: string; withPoll?: boolean; withSignup?: boolean; withBookingPage?: boolean } = {},
): Promise<SeededUser> {
  const response = await request.post('/api/test/seed', {
    data: {
      name: opts.name ?? 'E2E User',
      withPoll: opts.withPoll ?? false,
      withSignup: opts.withSignup ?? false,
      withBookingPage: opts.withBookingPage ?? false,
    },
  })
  if (!response.ok()) {
    throw new Error(
      `POST /api/test/seed responded ${response.status()} — is ENABLE_TEST_ROUTES=true in .dev.vars?`,
    )
  }
  return (await response.json()) as SeededUser
}

type Fixtures = {
  /** A verified user with no polls of their own. */
  user: SeededUser
  /** A verified user that already owns one seeded two-option datetime poll. */
  userWithPoll: SeededUser
  /** A verified user that already owns one seeded sign-up sheet (Slot 1 capacity 1, Slot 2 unlimited). */
  userWithSignup: SeededUser
  /**
   * A verified user with a handle and one seeded booking page: weekday 09:00–17:00
   * Europe/Oslo, 30-minute slots, slug `intro-call` — see `sampleBookingPage` in
   * `src/routes/api/test/seed.ts`.
   */
  userWithBookingPage: SeededUser
}

/**
 * Every fixture seeds its own user via the test-only `/api/test/seed` route (gated to
 * non-production builds with `ENABLE_TEST_ROUTES=true`, see `.dev.vars.example`), so specs never
 * share state and can run in any order or in parallel.
 */
// The fixture callback's second parameter is named `provide` rather than Playwright's usual
// `use` so `eslint-plugin-react-hooks` doesn't mistake `use(...)` for a React hook call — this
// file has no React in it.
export const test = base.extend<Fixtures>({
  user: async ({ request }, provide) => {
    await provide(await seed(request))
  },
  userWithPoll: async ({ request }, provide) => {
    await provide(await seed(request, { withPoll: true }))
  },
  userWithSignup: async ({ request }, provide) => {
    await provide(await seed(request, { withSignup: true }))
  },
  userWithBookingPage: async ({ request }, provide) => {
    await provide(await seed(request, { withBookingPage: true }))
  },
})

export { expect }

/**
 * Waits for the Cloudflare Turnstile *test* widget (site key `1x0000...AA`, always passes) to
 * finish its automatic challenge. Every Turnstile integration — including
 * `@marsidev/react-turnstile` — writes the solved token into a hidden
 * `input[name="cf-turnstile-response"]` once verified, so polling that value works regardless of
 * widget theme/size and doesn't depend on any particular loading UI.
 */
export async function waitForTurnstile(page: Page): Promise<void> {
  await expect
    .poll(
      () =>
        page.evaluate(() => {
          const input = document.querySelector<HTMLInputElement>(
            'input[name="cf-turnstile-response"]',
          )
          return input?.value ?? ''
        }),
      { timeout: 15_000, message: 'waiting for the Turnstile test widget to auto-solve' },
    )
    .not.toBe('')
}

/**
 * Waits until the React tree has hydrated (the root layout sets `data-hydrated` on <html> after
 * mount). Typing into an SSR'd controlled input before that point is silently discarded when
 * React attaches, which is the classic source of "I filled it but it's empty" flakes.
 */
export async function waitForHydration(page: Page): Promise<void> {
  await page.locator('html[data-hydrated="true"]').waitFor({ state: 'attached', timeout: 15_000 })
}

/**
 * Signs in through the real login form (fills the fields, waits out the Turnstile test widget,
 * submits) and waits for the post-login redirect.
 */
export async function signIn(
  page: Page,
  user: Pick<SeededUser, 'email' | 'password'>,
  opts: { next?: string } = {},
): Promise<void> {
  await page.goto(opts.next ? `/login?next=${encodeURIComponent(opts.next)}` : '/login')
  await waitForHydration(page)
  await page.locator('#login-email').fill(user.email)
  await page.locator('#login-password').fill(user.password)
  await waitForTurnstile(page)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForURL(opts.next ?? '**/dashboard')
}

/**
 * Picks the two earliest enabled days on the poll creator's calendar (`DateOptionsEditor`),
 * paging forward a month at a time if the visible month doesn't have two selectable days yet —
 * which keeps the test correct no matter what day of the month CI happens to run on. Days before
 * today are disabled by the app itself, so every button this finds is already a valid choice.
 */
export async function pickTwoCalendarDays(page: Page): Promise<void> {
  const calendar = page.locator('[data-slot="calendar"]')
  await expect(calendar).toBeVisible()

  const enabledDays = calendar.locator('button[data-day]:not([disabled])')
  for (let guard = 0; guard < 6 && (await enabledDays.count()) < 2; guard++) {
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
  }
  await expect(async () => {
    expect(await enabledDays.count()).toBeGreaterThanOrEqual(2)
  }).toPass({ timeout: 5_000 })

  await enabledDays.nth(0).click()
  await enabledDays.nth(1).click()
}

/**
 * Picks the first enabled day on the public booking page's month picker (`MonthPicker`, built on
 * the same `Calendar` primitive `pickTwoCalendarDays` drives for the poll creator), paging
 * forward a month at a time if the visible month has none yet. The seeded page (weekday
 * 09:00–17:00 Europe/Oslo, `min_notice_min: 0`) always has an enabled day within a couple of
 * months, so this never depends on which day of the month the suite happens to run on.
 */
export async function pickFirstEnabledDay(page: Page): Promise<void> {
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
