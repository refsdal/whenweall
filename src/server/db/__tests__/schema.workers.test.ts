import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { polls, pollOptions, user, votes, participants } from '#/server/db/schema'

describe('schema', () => {
  it('inserts a user, a poll with options, a participant and votes, and cascades on delete', async () => {
    const db = createDb(env.DB)
    const now = new Date().toISOString()
    await db.insert(user).values({
      id: 'u1',
      name: 'Ada',
      email: 'ada@example.com',
      emailVerified: true,
      createdAt: new Date(),
      updatedAt: new Date(),
    })
    await db.insert(polls).values({
      id: 'p'.repeat(12),
      ownerId: 'u1',
      type: 'options',
      title: 'Lunch',
      timezone: 'Europe/Oslo',
      createdAt: now,
      updatedAt: now,
    })
    await db.insert(pollOptions).values([
      { id: 'o1', pollId: 'p'.repeat(12), position: 0, kind: 'text', label: 'Pizza' },
      { id: 'o2', pollId: 'p'.repeat(12), position: 1, kind: 'text', label: 'Sushi' },
    ])
    await db
      .insert(participants)
      .values({ id: 'pa1', pollId: 'p'.repeat(12), name: 'Bob', createdAt: now, updatedAt: now })
    await db.insert(votes).values([
      { participantId: 'pa1', optionId: 'o1', answer: 'yes' },
      { participantId: 'pa1', optionId: 'o2', answer: 'no' },
    ])

    const loaded = await db.query.polls.findFirst({
      where: eq(polls.id, 'p'.repeat(12)),
      with: { options: true, participants: { with: { votes: true } } },
    })
    expect(loaded?.options).toHaveLength(2)
    expect(loaded?.participants[0]?.votes).toHaveLength(2)
    expect(loaded?.status).toBe('open')

    await db.delete(polls).where(eq(polls.id, 'p'.repeat(12)))
    expect(await db.select().from(votes)).toHaveLength(0)
    expect(await db.select().from(pollOptions)).toHaveLength(0)
  })

  it('inserts a signup poll with signupMaxClaims and an option with capacity, and reads them back', async () => {
    const db = createDb(env.DB)
    const now = new Date().toISOString()
    await db.insert(polls).values({
      id: 's'.repeat(12),
      ownerId: 'u1',
      type: 'signup',
      title: 'Bring a dish',
      timezone: 'Europe/Oslo',
      signupMaxClaims: 3,
      createdAt: now,
      updatedAt: now,
    })
    await db.insert(pollOptions).values([
      {
        id: 'so1',
        pollId: 's'.repeat(12),
        position: 0,
        kind: 'text',
        label: 'Salad',
        capacity: 5,
      },
    ])

    const loaded = await db.query.polls.findFirst({
      where: eq(polls.id, 's'.repeat(12)),
      with: { options: true },
    })
    expect(loaded?.type).toBe('signup')
    expect(loaded?.signupMaxClaims).toBe(3)
    expect(loaded?.options[0]?.capacity).toBe(5)
  })
})
