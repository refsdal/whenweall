import {
  test as base,
  expect,
  type APIRequestContext,
  type Locator,
  type Page,
} from '@playwright/test'

/** What `POST /api/test/seed` (internal/httpserver/testroutes.go) hands back for a freshly created,
 * already-verified user. Optional fields are `null` in the JSON unless the matching `with*` flag was
 * sent — the typed fixtures below turn a missing one into a hard failure. */
export type SeededUser = {
  email: string
  password: string
  name: string
  pollId?: string | null
  pageId?: string | null
  handle?: string | null
  slug?: string | null
  failedJobId?: string | null
}

export type SeedOptions = {
  name?: string
  withPoll?: boolean
  withSignup?: boolean
  withBookingPage?: boolean
  role?: 'staff'
  /** Also insert one dead-lettered `scheduled_jobs` row (attempts == max_attempts) and return its id. */
  failedJob?: boolean
}

async function seed(request: APIRequestContext, opts: SeedOptions = {}): Promise<SeededUser> {
  const response = await request.post('/api/test/seed', {
    data: {
      name: opts.name ?? 'E2E User',
      withPoll: opts.withPoll ?? false,
      withSignup: opts.withSignup ?? false,
      withBookingPage: opts.withBookingPage ?? false,
      failedJob: opts.failedJob ?? false,
      ...(opts.role ? { role: opts.role } : {}),
    },
  })
  if (!response.ok()) {
    throw new Error(
      `POST /api/test/seed responded ${response.status()}: ${await response.text()} — the e2e server ` +
        `must run with ENABLE_TEST_ROUTES=true (playwright.config.ts webServer.env / e2e/compose.e2e.yaml)`,
    )
  }
  return (await response.json()) as SeededUser
}

/** Narrows a seed result: the named fields must be non-empty strings, else the fixture fails loudly
 * (a `test.skip` here would silently hide a broken seed route behind a green run). */
function requireFields<K extends keyof SeededUser>(
  seeded: SeededUser,
  keys: K[],
): SeededUser & { [P in K]: string } {
  for (const key of keys) {
    if (typeof seeded[key] !== 'string' || seeded[key] === '') {
      throw new Error(`POST /api/test/seed did not return "${String(key)}": ${JSON.stringify(seeded)}`)
    }
  }
  return seeded as SeededUser & { [P in K]: string }
}

type Fixtures = {
  /** A verified user with no polls of their own. */
  user: SeededUser
  /** A verified user that already owns one seeded two-option datetime poll ("Seeded test poll",
   * Europe/Oslo, comments allowed — internal/polls/seed.go). */
  userWithPoll: SeededUser & { pollId: string }
  /** A verified user that already owns one seeded sign-up sheet (Slot 1 capacity 1, Slot 2 unlimited). */
  userWithSignup: SeededUser & { pollId: string }
  /**
   * A verified user with a handle and one seeded booking page: weekday 09:00–17:00 Europe/Oslo,
   * 30-minute slots, slug `intro-call` — see `CreateSampleBookingPage` in internal/bookings/seed.go.
   */
  userWithBookingPage: SeededUser & { pageId: string; handle: string; slug: string }
  /** A verified user carrying the platform staff role, for the admin console. */
  userStaff: SeededUser
  /** A staff user plus one dead-lettered job the console's Jobs page can list and retry. */
  userStaffWithFailedJob: SeededUser & { failedJobId: string }
}

/**
 * Every fixture seeds its own user via the test-only `/api/test/seed` route (only mounted when the
 * server runs with `ENABLE_TEST_ROUTES=true`, which config.Load refuses alongside
 * APP_ENV=production), so specs never share state and can run in any order or in parallel.
 */
// The fixture callback's second parameter is named `provide` rather than Playwright's usual
// `use` so `eslint-plugin-react-hooks` doesn't mistake `use(...)` for a React hook call — this
// file has no React in it.
export const test = base.extend<Fixtures>({
  user: async ({ request }, provide) => {
    await provide(await seed(request))
  },
  userWithPoll: async ({ request }, provide) => {
    await provide(requireFields(await seed(request, { withPoll: true }), ['pollId']))
  },
  userWithSignup: async ({ request }, provide) => {
    await provide(requireFields(await seed(request, { withSignup: true }), ['pollId']))
  },
  userWithBookingPage: async ({ request }, provide) => {
    await provide(
      requireFields(await seed(request, { withBookingPage: true }), ['pageId', 'handle', 'slug']),
    )
  },
  userStaff: async ({ request }, provide) => {
    await provide(await seed(request, { role: 'staff' }))
  },
  userStaffWithFailedJob: async ({ request }, provide) => {
    await provide(
      requireFields(await seed(request, { role: 'staff', failedJob: true }), ['failedJobId']),
    )
  },
})

export { expect }

/**
 * Waits until the React tree has mounted (the root layout sets `data-hydrated` on <html> after
 * mount — web/src/routes/__root.tsx). Typing into a controlled input before that point is silently
 * discarded when React attaches, which is the classic source of "I filled it but it's empty" flakes.
 */
export async function waitForHydration(page: Page): Promise<void> {
  await page.locator('html[data-hydrated="true"]').waitFor({ state: 'attached', timeout: 15_000 })
}

/**
 * Signs in through the real login form (fills the fields, submits) and waits for the post-login
 * redirect. Turnstile is off in the e2e server, so there is no widget to wait out.
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
 * Picks the first enabled day on a booking month picker (`MonthPicker`, built on the same
 * `Calendar` primitive `pickTwoCalendarDays` drives), paging forward a month at a time if the
 * visible month has none yet. `scope` may be a Page or any Locator that contains exactly one
 * calendar — e.g. the reschedule dialog, which has its own picker next to the page's.
 */
export async function pickFirstEnabledDay(scope: Page | Locator): Promise<void> {
  const calendar = scope.locator('[data-slot="calendar"]')
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

/**
 * Like `pickFirstEnabledDay`, but never picks "today" — deliberately so a caller that books
 * today's only remaining slot doesn't have to worry about `SlotPicker`'s own documented "no slots
 * left on this day, jump to the next enabled one" fallback
 * (`web/src/components/booking/SlotPicker.tsx`) coincidentally matching an analogous same-time
 * chip on a different day. "Today" is the one day whose slot count can already be partway drained
 * by the time the suite runs (most of a day's 09:00–17:00 Europe/Oslo weekday window may already
 * be in the past by wall-clock afternoon) — any OTHER day is always a full, untouched day, so
 * booking one slot on it can never empty the whole day.
 *
 * Picks the LAST enabled day in the currently visible month rather than the first: in the
 * overwhelmingly common case that's a different day from "today" already, at no extra request
 * cost over `pickFirstEnabledDay` — booking.spec.ts's own two browser contexts each cost two
 * loader-driven fetches just to land on this page, against the public booking rate limiter
 * (internal/bookings/handlers.go), so an unconditional month-forward navigation (an extra fetch
 * pair per context) is a real budget concern, not a style preference. Only pages forward (same
 * fallback loop as `pickFirstEnabledDay`) on the rare day the last enabled day in view IS today —
 * matched via `toLocaleDateString('en-US')` against the `data-day` attribute
 * (`web/src/components/ui/calendar.tsx`), which reads it from a `Date` in the SAME locale
 * (Playwright's own context option, `playwright.config.ts`) and system time zone a plain
 * `new Date()` in this Node process already uses.
 */
export async function pickFirstEnabledDayNotToday(scope: Page | Locator): Promise<void> {
  const calendar = scope.locator('[data-slot="calendar"]')
  await expect(calendar).toBeVisible()

  const todayKey = new Date().toLocaleDateString('en-US')

  const lastNonTodayDay = async () => {
    const enabledDays = calendar.locator('button[data-day]:not([disabled])')
    const count = await enabledDays.count()
    for (let i = count - 1; i >= 0; i--) {
      const day = enabledDays.nth(i)
      if ((await day.getAttribute('data-day')) !== todayKey) return day
    }
    return null
  }

  let target = await lastNonTodayDay()
  for (let guard = 0; guard < 6 && target === null; guard++) {
    await calendar.getByRole('button', { name: 'Go to the Next Month' }).click()
    await expect(async () => {
      const enabledDays = calendar.locator('button[data-day]:not([disabled])')
      expect(await enabledDays.count()).toBeGreaterThanOrEqual(1)
    }).toPass({ timeout: 5_000 })
    target = await lastNonTodayDay()
  }
  if (!target) {
    throw new Error('pickFirstEnabledDayNotToday: no non-today enabled day found within 6 months')
  }
  await target.click()
}
