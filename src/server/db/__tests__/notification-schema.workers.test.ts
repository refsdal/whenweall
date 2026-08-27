import { env } from 'cloudflare:workers'
import { and, eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { notificationPrefs, notificationSubscriptions, pushSubscriptions } from '#/server/db/schema'
import { makePoll, makeUserWithOrg } from '../../../../test/helpers'

const now = () => new Date().toISOString()

describe('notification tables', () => {
  it('stores a grid as JSON and reads it back typed', async () => {
    const db = createDb(env.DB)
    const { userId } = await makeUserWithOrg(db)

    await db.insert(notificationPrefs).values({
      userId,
      channels: { 'response.created': { email: false, push: true } },
      createdAt: now(),
      updatedAt: now(),
    })

    const row = await db.query.notificationPrefs.findFirst({
      where: eq(notificationPrefs.userId, userId),
    })
    expect(row?.channels?.['response.created']).toEqual({ email: false, push: true })
  })

  it('treats a null channels column as "inherit my defaults"', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeUserWithOrg(db)
    // `createPoll` subscribes the creator itself, so there is no row to insert here — asserting
    // on what it wrote is a stronger check than asserting on a row this test fabricated.
    const { id: pollId } = await makePoll(db, { orgId, createdBy: userId })

    const row = await db.query.notificationSubscriptions.findFirst({
      where: and(
        eq(notificationSubscriptions.scopeId, pollId),
        eq(notificationSubscriptions.userId, userId),
      ),
    })
    expect(row?.channels ?? null).toBeNull()
    expect(row?.source).toBe('creator')
  })

  it('keys subscriptions by scope so the same user can follow a poll and a page', async () => {
    const db = createDb(env.DB)
    const { userId } = await makeUserWithOrg(db)

    await db.insert(notificationSubscriptions).values([
      {
        scopeType: 'poll',
        scopeId: 'scope-shared-id',
        userId,
        source: 'follow',
        channels: null,
        createdAt: now(),
        updatedAt: now(),
      },
      {
        scopeType: 'booking_page',
        scopeId: 'scope-shared-id',
        userId,
        source: 'follow',
        channels: null,
        createdAt: now(),
        updatedAt: now(),
      },
    ])

    const rows = await db.query.notificationSubscriptions.findMany({
      where: eq(notificationSubscriptions.scopeId, 'scope-shared-id'),
    })
    expect(rows.map((r) => r.scopeType).sort()).toEqual(['booking_page', 'poll'])
  })

  it('rejects a duplicate push endpoint', async () => {
    const db = createDb(env.DB)
    const { userId } = await makeUserWithOrg(db)
    const row = {
      userId,
      endpoint: 'https://push.example/abc',
      p256dh: 'key',
      auth: 'auth',
      userAgent: null,
      createdAt: now(),
      lastSeenAt: now(),
    }

    await db.insert(pushSubscriptions).values({ id: 'push-1', ...row })
    await expect(db.insert(pushSubscriptions).values({ id: 'push-2', ...row })).rejects.toThrow()
  })
})

describe('backfill expression', () => {
  // The migration's INSERT..SELECT runs against an empty `polls` table in a fresh test database,
  // which proves it parses but not that it builds the right JSON. Run the same expression against
  // known inputs so a wrong mapping fails here rather than on a production organiser's inbox.
  it('maps the dropped booleans onto the grid shape the app reads back', async () => {
    const sql = `SELECT json_object(
      'response.created', json_object('email', json(CASE WHEN ?1 THEN 'true' ELSE 'false' END), 'push', json('false')),
      'response.updated', json_object('email', json(CASE WHEN ?1 THEN 'true' ELSE 'false' END), 'push', json('false')),
      'comment.created', json_object('email', json(CASE WHEN ?2 THEN 'true' ELSE 'false' END), 'push', json('false'))
    ) AS channels`

    const votesOff = await env.DB.prepare(sql).bind(0, 1).first<{ channels: string }>()
    expect(JSON.parse(votesOff!.channels)).toEqual({
      'response.created': { email: false, push: false },
      'response.updated': { email: false, push: false },
      'comment.created': { email: true, push: false },
    })

    const allOn = await env.DB.prepare(sql).bind(1, 1).first<{ channels: string }>()
    expect(JSON.parse(allOn!.channels)['response.updated']).toEqual({ email: true, push: false })
  })
})
