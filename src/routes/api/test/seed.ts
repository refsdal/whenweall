import { createFileRoute } from '@tanstack/react-router'
import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { getAuth } from '#/server/auth/auth'
import { getDb } from '#/server/db/client'
import { member, subscription } from '#/server/db/schema'
import * as pollService from '#/server/polls/service'
import { createPage, setOrgSlug } from '#/server/bookings/pages'
import type { CreateBookingPageInput } from '#/server/bookings/schemas'

type SeedBody = {
  email?: string
  name?: string
  password?: string
  withPoll?: boolean
  withSignup?: boolean
  withBookingPage?: boolean
  /** Inserts an active premium subscription row for the seeded org, so e2e can exercise premium
   * billing states (seat usage, "Manage billing", etc.) without going through real Stripe. */
  plan?: 'premium'
}

/** Weekday (Mon–Fri) 09:00–17:00, 30-min slots, Europe/Oslo — mirrors `test/helpers.ts`'
 * `makeBookingPage` fixture, kept independent since that helper lives outside `src/`. */
function sampleBookingPage(): CreateBookingPageInput {
  const weekday = [{ start: '09:00', end: '17:00' }]
  return {
    slug: 'intro-call',
    title: 'Intro call',
    timezone: 'Europe/Oslo',
    slotDurationMin: 30,
    bufferBeforeMin: 0,
    bufferAfterMin: 0,
    minNoticeMin: 0,
    maxDaysAhead: 60,
    availability: { '1': weekday, '2': weekday, '3': weekday, '4': weekday, '5': weekday },
    googleSync: false,
    reminders: true,
  }
}

/** Two datetime options, a couple of days out, so the seeded poll looks like a real one. */
function samplePollOptions(): { kind: 'datetime'; startAt: string; endAt: string }[] {
  const base = new Date()
  base.setUTCHours(16, 30, 0, 0)
  return [2, 3].map((offset) => {
    const start = new Date(base.getTime() + offset * 24 * 60 * 60 * 1000)
    const end = new Date(start.getTime() + 90 * 60 * 1000)
    return { kind: 'datetime', startAt: start.toISOString(), endAt: end.toISOString() }
  })
}

/**
 * The actual seeding logic, split from the route's POST handler so a workers test can exercise it
 * directly without also having to flip on `ENABLE_TEST_ROUTES` (which isn't part of
 * `test/wrangler.test.jsonc` — this route's production/staging gate, checked in the handler
 * below, is exactly what a test bypassing it would otherwise be fighting). Same extraction
 * pattern as `rosterResponse`/`bookingIcsResponse`.
 */
export async function seedResponse(body: SeedBody): Promise<Response> {
  const email = body.email ?? `test-${crypto.randomUUID()}@example.com`
  const name = body.name ?? 'Test User'
  const password = body.password ?? 'correct horse battery staple'

  const signUp = await getAuth().api.signUpEmail({
    body: { name, email, password, locale: 'en' },
    headers: new Headers({ 'x-captcha-response': 'test' }),
  })

  await env.DB.prepare('update user set email_verified = 1 where email = ?').bind(email).run()

  // Every signup auto-gets a personal organization (Task 1) — content ownership lives there now,
  // so seeded content is created under that org rather than directly under the user.
  const db = getDb()
  const membership = await db.query.member.findFirst({ where: eq(member.userId, signUp.user.id) })
  const organizationId = membership!.organizationId
  const createdBy = signUp.user.id

  let pollId: string | null = null
  if (body.withPoll) {
    const created = await pollService.createPoll(
      db,
      { organizationId, createdBy },
      {
        type: 'datetime',
        title: 'Seeded test poll',
        description: 'Created by the test seed route.',
        timezone: 'Europe/Oslo',
        options: samplePollOptions(),
        allowComments: true,
        allowIfNeedBe: true,
        requireParticipantEmail: false,
      },
    )
    pollId = created.id
  }
  // Two text slots — one capped at 1, one unlimited — enough for an e2e test to fill a slot, see
  // it go "full", and still claim the other one.
  if (body.withSignup) {
    const created = await pollService.createPoll(
      db,
      { organizationId, createdBy },
      {
        type: 'signup',
        title: 'Seeded sign-up sheet',
        description: 'Created by the test seed route.',
        timezone: 'Europe/Oslo',
        options: [
          { kind: 'text', label: 'Slot 1', capacity: 1 },
          { kind: 'text', label: 'Slot 2', capacity: null },
        ],
      },
    )
    pollId = created.id
  }

  let pageId: string | null = null
  let handle: string | null = null
  let slug: string | null = null
  if (body.withBookingPage) {
    handle = `test-${crypto.randomUUID().slice(0, 8)}`
    await setOrgSlug(db, organizationId, handle)
    const page = sampleBookingPage()
    const created = await createPage(db, { organizationId, createdBy }, page)
    pageId = created.id
    slug = page.slug
  }

  if (body.plan === 'premium') {
    await db.insert(subscription).values({
      id: crypto.randomUUID(),
      plan: 'premium',
      referenceId: organizationId,
      status: 'active',
      periodEnd: new Date(Date.now() + 30 * 24 * 60 * 60 * 1000),
      cancelAtPeriodEnd: false,
    })
  }

  return Response.json({ email, password, name, pollId, pageId, handle, slug })
}

/**
 * Test-only route used by Playwright to create a verified user without going through the real
 * email-verification flow, and — with `withPoll`/`withSignup`/`withBookingPage` — content owned
 * by that user. Gated so it can never be reachable in production even if ENABLE_TEST_ROUTES is
 * accidentally left set.
 */
export const Route = createFileRoute('/api/test/seed')({
  server: {
    handlers: {
      POST: async ({ request }) => {
        const appEnv: string = env.APP_ENV
        if (appEnv === 'production' || env.ENABLE_TEST_ROUTES !== 'true') {
          return new Response('Not found', { status: 404 })
        }

        const body = (await request.json().catch(() => ({}))) as SeedBody
        return seedResponse(body)
      },
    },
  },
})
