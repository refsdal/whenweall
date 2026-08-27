import type { Db } from '#/server/db/client'
import {
  bookings,
  invitation,
  member,
  organization,
  participants,
  subscription,
  user,
  votes,
  type BookingStatus,
  type CancelledBy,
} from '#/server/db/schema'
import { newId } from '#/lib/ids'
import type { Answer } from '#/lib/scoring'
import { createPoll } from '#/server/polls/service'
import type { CreatePollInput } from '#/server/polls/schemas'
import { createPage } from '#/server/bookings/pages'
import type { CreateBookingPageInput } from '#/server/bookings/schemas'
import { generateToken, hashToken } from '#/lib/tokens'
import { chunkedInsert } from '#/server/db/chunked-insert'

let counter = 0
function unique(): string {
  counter += 1
  return `${Date.now()}-${counter}`
}

export async function makeUser(
  db: Db,
  overrides?: Partial<{ id: string; name: string; email: string; locale: string | null }>,
): Promise<{ id: string; email: string }> {
  const id = overrides?.id ?? `user_${newId()}`
  const email = overrides?.email ?? `user-${unique()}@example.com`
  const now = new Date()
  await db.insert(user).values({
    id,
    name: overrides?.name ?? 'Test User',
    email,
    emailVerified: true,
    locale: overrides?.locale ?? null,
    createdAt: now,
    updatedAt: now,
  })
  return { id, email }
}

export async function makeOrg(
  db: Db,
  ownerUserId: string,
  overrides?: Partial<{ name: string; slug: string; role: 'owner' | 'admin' | 'member' }>,
): Promise<{ id: string; slug: string }> {
  const id = `org_${newId()}`
  const slug = overrides?.slug ?? `org-${unique()}`
  const now = new Date()
  await db
    .insert(organization)
    .values({ id, name: overrides?.name ?? 'Test Org', slug, createdAt: now })
  await db.insert(member).values({
    id: `mem_${newId()}`,
    organizationId: id,
    userId: ownerUserId,
    role: overrides?.role ?? 'owner',
    createdAt: now,
  })
  return { id, slug }
}

/** Adds `userId` to an existing org as an additional member (default role `'member'`) — for
 * tests that need a second, non-owner member of the same org (e.g. asserting FORBIDDEN for a
 * plain member managing someone else's content, as distinct from NOT_FOUND for a different org
 * entirely). */
export async function addOrgMember(
  db: Db,
  orgId: string,
  userId: string,
  role: 'owner' | 'admin' | 'member' = 'member',
): Promise<void> {
  await db.insert(member).values({
    id: `mem_${newId()}`,
    organizationId: orgId,
    userId,
    role,
    createdAt: new Date(),
  })
}

/** `makeUser` + `makeOrg`: the common "one user owning one fresh org" fixture most tests build
 * on, now that content ownership lives on organizations rather than directly on a user. */
export async function makeUserWithOrg(
  db: Db,
  overrides?: Parameters<typeof makeUser>[1],
): Promise<{ userId: string; orgId: string; slug: string }> {
  const { id: userId } = await makeUser(db, overrides)
  const { id: orgId, slug } = await makeOrg(db, userId)
  return { userId, orgId, slug }
}

export async function makePoll(
  db: Db,
  own: { orgId: string; createdBy?: string | null },
  overrides?: Partial<CreatePollInput>,
): Promise<{ id: string }> {
  const tomorrow = new Date(Date.now() + 24 * 60 * 60 * 1000)
  const day = tomorrow.toISOString().slice(0, 10)
  const input: CreatePollInput = {
    type: 'datetime',
    title: 'Team sync',
    timezone: 'Europe/Oslo',
    options: [
      { kind: 'datetime', startAt: `${day}T10:00:00.000Z` },
      { kind: 'datetime', startAt: `${day}T11:00:00.000Z` },
    ],
    ...overrides,
  }
  return createPoll(db, { organizationId: own.orgId, createdBy: own.createdBy ?? null }, input)
}

export async function makeSignupPoll(
  db: Db,
  own: { orgId: string; createdBy?: string | null },
  opts: { capacities: (number | null)[]; maxClaims?: number; requireEmail?: boolean },
): Promise<{ id: string }> {
  const input: CreatePollInput = {
    type: 'signup',
    title: 'Sign-up sheet',
    timezone: 'Europe/Oslo',
    options: opts.capacities.map((capacity, i) => ({
      kind: 'text',
      label: `Slot ${i + 1}`,
      capacity,
    })),
    signupMaxClaims: opts.maxClaims,
    requireParticipantEmail: opts.requireEmail,
  }
  return createPoll(db, { organizationId: own.orgId, createdBy: own.createdBy ?? null }, input)
}

export async function makeParticipant(
  db: Db,
  pollId: string,
  name: string,
  answers: Record<string, Answer>,
  overrides?: Partial<{ email: string | null; userId: string | null }>,
): Promise<{ id: string }> {
  const id = `pa_${newId()}`
  const now = new Date().toISOString()
  await db.insert(participants).values({
    id,
    pollId,
    name,
    email: overrides?.email ?? null,
    userId: overrides?.userId ?? null,
    createdAt: now,
    updatedAt: now,
  })
  const rows = Object.entries(answers).map(([optionId, answer]) => ({
    participantId: id,
    optionId,
    answer,
  }))
  const inserts = chunkedInsert(db, votes, rows)
  if (inserts.length > 0) {
    await db.batch(inserts as [(typeof inserts)[number], ...(typeof inserts)[number][]])
  }
  return { id }
}

/** Weekday (Mon–Fri) availability, 09:00–17:00, 30-min slots, Europe/Oslo — the default fixture
 * most booking tests build on, overridable per-field. `memberUserId` defaults to `createdBy`
 * (matching `createPage`'s own default) when omitted. */
export async function makeBookingPage(
  db: Db,
  own: { orgId: string; memberUserId?: string | null; createdBy?: string | null },
  overrides?: Partial<CreateBookingPageInput>,
): Promise<{ id: string }> {
  const weekday = [{ start: '09:00', end: '17:00' }]
  const input: CreateBookingPageInput = {
    slug: `page-${unique()}`,
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
    ...overrides,
  }
  return createPage(
    db,
    {
      organizationId: own.orgId,
      createdBy: own.createdBy ?? null,
      memberUserId: own.memberUserId,
    },
    input,
  )
}

/** Inserts a `bookings` row directly (bypassing `createBooking`'s validation) so tests can seed
 * fixtures — e.g. an existing booking to collide with — without depending on `createBooking`
 * itself. Defaults to a 30-minute slot and a freshly generated (and hashed) manage token. */
export async function makeBooking(
  db: Db,
  pageId: string,
  startAt: string,
  overrides?: Partial<{
    endAt: string
    visitorName: string
    visitorEmail: string
    visitorNote: string | null
    visitorLocale: string | null
    visitorTimezone: string
    status: BookingStatus
    cancelledBy: CancelledBy | null
    manageTokenHash: string
    googleEventId: string | null
  }>,
): Promise<{ id: string; manageToken: string }> {
  const id = `bk_${newId()}`
  const now = new Date().toISOString()
  const manageToken = generateToken()
  const manageTokenHash = overrides?.manageTokenHash ?? (await hashToken(manageToken))
  const endAt =
    overrides?.endAt ?? new Date(new Date(startAt).getTime() + 30 * 60_000).toISOString()

  await db.insert(bookings).values({
    id,
    pageId,
    startAt,
    endAt,
    visitorName: overrides?.visitorName ?? 'Visitor',
    visitorEmail: overrides?.visitorEmail ?? `visitor-${unique()}@example.com`,
    visitorNote: overrides?.visitorNote ?? null,
    visitorLocale: overrides?.visitorLocale ?? null,
    visitorTimezone: overrides?.visitorTimezone ?? 'Europe/Oslo',
    status: overrides?.status ?? 'confirmed',
    cancelledBy: overrides?.cancelledBy ?? null,
    manageTokenHash,
    googleEventId: overrides?.googleEventId ?? null,
    createdAt: now,
    updatedAt: now,
  })

  return { id, manageToken }
}

/** Inserts a bare `invitation` row directly (bypassing `auth.api.createInvitation`'s seat gate
 * and email send) — for tests that just need a pending/accepted invitation row to exist, e.g.
 * seat-usage counting. Defaults to `status: 'pending'` and a far-future `expiresAt`. */
export async function makeInvitation(
  db: Db,
  orgId: string,
  inviterId: string,
  overrides?: Partial<{ email: string; role: string; status: string; expiresAt: Date }>,
): Promise<{ id: string }> {
  const id = `inv_${newId()}`
  await db.insert(invitation).values({
    id,
    organizationId: orgId,
    email: overrides?.email ?? `invitee-${unique()}@example.com`,
    role: overrides?.role ?? 'member',
    status: overrides?.status ?? 'pending',
    expiresAt: overrides?.expiresAt ?? new Date(Date.now() + 7 * 24 * 60 * 60 * 1000),
    inviterId,
  })
  return { id }
}

export async function makeSubscription(
  db: Db,
  orgId: string,
  overrides?: Partial<{
    plan: string
    status: string
    stripeCustomerId: string | null
    stripeSubscriptionId: string | null
  }>,
): Promise<{ id: string }> {
  const id = `sub_${newId()}`
  await db.insert(subscription).values({
    id,
    plan: overrides?.plan ?? 'premium',
    referenceId: orgId,
    status: overrides?.status ?? 'active',
    stripeCustomerId: overrides?.stripeCustomerId ?? null,
    stripeSubscriptionId: overrides?.stripeSubscriptionId ?? null,
  })
  return { id }
}
