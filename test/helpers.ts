import type { Db } from '#/server/db/client'
import {
  bookings,
  participants,
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

export async function makePoll(
  db: Db,
  ownerId: string,
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
  return createPoll(db, ownerId, input)
}

export async function makeSignupPoll(
  db: Db,
  ownerId: string,
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
  return createPoll(db, ownerId, input)
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
  if (rows.length > 0) {
    await db.insert(votes).values(rows)
  }
  return { id }
}

/** Weekday (Mon–Fri) availability, 09:00–17:00, 30-min slots, Europe/Oslo — the default fixture
 * most booking tests build on, overridable per-field. */
export async function makeBookingPage(
  db: Db,
  ownerId: string,
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
  return createPage(db, ownerId, input)
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
