import type { Db } from '#/server/db/client'
import { participants, user, votes } from '#/server/db/schema'
import { newId } from '#/lib/ids'
import type { Answer } from '#/lib/scoring'
import { createPoll } from '#/server/polls/service'
import type { CreatePollInput } from '#/server/polls/schemas'

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
