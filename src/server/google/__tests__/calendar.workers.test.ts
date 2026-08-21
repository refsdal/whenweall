import { setupNetwork } from '@msw/cloudflare'
import { http, HttpResponse } from 'msw'
import { env } from 'cloudflare:workers'
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { account } from '#/server/db/schema'
import {
  createCalendarClient,
  getGoogleAccessToken,
  getGoogleCalendarStatus,
  GoogleApiError,
} from '#/server/google/calendar'
import { makeUser } from '../../../../test/helpers'

const network = setupNetwork()

beforeAll(() => network.enable())
afterEach(() => network.resetHandlers())
afterAll(() => network.disable())

const FREEBUSY_URL = 'https://www.googleapis.com/calendar/v3/freeBusy'
const EVENTS_URL = 'https://www.googleapis.com/calendar/v3/calendars/primary/events'

describe('getFreeBusy', () => {
  it('flattens calendars.primary.busy into intervals', async () => {
    network.use(
      http.post(FREEBUSY_URL, () =>
        HttpResponse.json({
          calendars: {
            primary: {
              busy: [
                { start: '2026-09-01T09:00:00.000Z', end: '2026-09-01T09:30:00.000Z' },
                { start: '2026-09-01T11:00:00.000Z', end: '2026-09-01T11:30:00.000Z' },
              ],
            },
          },
        }),
      ),
    )

    const client = createCalendarClient()
    const busy = await client.getFreeBusy('tok', {
      timeMin: '2026-09-01T00:00:00.000Z',
      timeMax: '2026-09-02T00:00:00.000Z',
    })

    expect(busy).toEqual([
      { start: '2026-09-01T09:00:00.000Z', end: '2026-09-01T09:30:00.000Z' },
      { start: '2026-09-01T11:00:00.000Z', end: '2026-09-01T11:30:00.000Z' },
    ])
  })

  it('throws GoogleApiError on a non-2xx response (e.g. an expired token)', async () => {
    network.use(http.post(FREEBUSY_URL, () => HttpResponse.json({}, { status: 401 })))

    const client = createCalendarClient()
    await expect(
      client.getFreeBusy('tok', {
        timeMin: '2026-09-01T00:00:00.000Z',
        timeMax: '2026-09-02T00:00:00.000Z',
      }),
    ).rejects.toBeInstanceOf(GoogleApiError)
  })
})

describe('createEvent', () => {
  it('returns the created event id', async () => {
    network.use(http.post(EVENTS_URL, () => HttpResponse.json({ id: 'evt_123' }, { status: 200 })))

    const client = createCalendarClient()
    const { eventId } = await client.createEvent('tok', {
      summary: '15 min intro',
      start: '2026-09-01T09:00:00.000Z',
      end: '2026-09-01T09:30:00.000Z',
      attendeeEmail: 'bob@example.com',
      timezone: 'Europe/Oslo',
    })

    expect(eventId).toBe('evt_123')
  })

  it('throws GoogleApiError on failure', async () => {
    network.use(http.post(EVENTS_URL, () => HttpResponse.json({}, { status: 500 })))

    const client = createCalendarClient()
    await expect(
      client.createEvent('tok', {
        summary: '15 min intro',
        start: '2026-09-01T09:00:00.000Z',
        end: '2026-09-01T09:30:00.000Z',
        attendeeEmail: 'bob@example.com',
        timezone: 'Europe/Oslo',
      }),
    ).rejects.toBeInstanceOf(GoogleApiError)
  })
})

describe('deleteEvent', () => {
  it('treats 404 as success', async () => {
    network.use(http.delete(`${EVENTS_URL}/evt_123`, () => HttpResponse.json({}, { status: 404 })))

    const client = createCalendarClient()
    await expect(client.deleteEvent('tok', 'evt_123')).resolves.toBeUndefined()
  })

  it('treats 410 as success', async () => {
    network.use(http.delete(`${EVENTS_URL}/evt_456`, () => HttpResponse.json({}, { status: 410 })))

    const client = createCalendarClient()
    await expect(client.deleteEvent('tok', 'evt_456')).resolves.toBeUndefined()
  })

  it('throws GoogleApiError on other failures', async () => {
    network.use(http.delete(`${EVENTS_URL}/evt_789`, () => HttpResponse.json({}, { status: 500 })))

    const client = createCalendarClient()
    await expect(client.deleteEvent('tok', 'evt_789')).rejects.toBeInstanceOf(GoogleApiError)
  })
})

describe('getGoogleAccessToken', () => {
  it('returns null when the user has no linked google account', async () => {
    const db = createDb(env.DB)
    const { id: userId } = await makeUser(db)

    await expect(getGoogleAccessToken(userId)).resolves.toBeNull()
  })

  it('degrades to null when a token cannot be obtained (e.g. Google not configured)', async () => {
    const db = createDb(env.DB)
    const { id: userId } = await makeUser(db)
    const now = new Date()
    await db.insert(account).values({
      id: 'acct_1',
      issuer: 'google',
      accountId: 'google-sub-1',
      providerId: 'google',
      userId,
      accessToken: 'stale-token',
      createdAt: now,
      updatedAt: now,
    })

    // The test environment has no GOOGLE_CLIENT_ID/SECRET configured, so Better-Auth has no
    // 'google' social provider registered and `getAccessToken` fails — this exercises the
    // catch-and-degrade path rather than a successful refresh.
    await expect(getGoogleAccessToken(userId)).resolves.toBeNull()
  })

  it('returns null when the linked account is missing a required calendar scope', async () => {
    const db = createDb(env.DB)
    const { id: userId } = await makeUser(db)
    const now = new Date()
    await db.insert(account).values({
      id: 'acct_2',
      issuer: 'google',
      accountId: 'google-sub-2',
      providerId: 'google',
      userId,
      accessToken: 'some-token',
      // openid/email/profile only — no calendar.readonly or calendar.events.
      scope: 'openid email profile',
      createdAt: now,
      updatedAt: now,
    })

    await expect(getGoogleAccessToken(userId)).resolves.toBeNull()
  })
})

describe('getGoogleCalendarStatus', () => {
  it('is not connected when there is no linked google account', async () => {
    const db = createDb(env.DB)
    const { id: userId } = await makeUser(db)

    await expect(getGoogleCalendarStatus(userId)).resolves.toEqual({ connected: false })
  })

  it('is not connected when the granted scope is missing a calendar scope', async () => {
    const db = createDb(env.DB)
    const { id: userId } = await makeUser(db)
    const now = new Date()
    await db.insert(account).values({
      id: 'acct_3',
      issuer: 'google',
      accountId: 'google-sub-3',
      providerId: 'google',
      userId,
      accessToken: 'some-token',
      scope: 'openid email https://www.googleapis.com/auth/calendar.readonly',
      createdAt: now,
      updatedAt: now,
    })

    await expect(getGoogleCalendarStatus(userId)).resolves.toEqual({ connected: false })
  })

  it('is connected when the granted scope covers both calendar scopes', async () => {
    const db = createDb(env.DB)
    const { id: userId } = await makeUser(db)
    const now = new Date()
    await db.insert(account).values({
      id: 'acct_4',
      issuer: 'google',
      accountId: 'google-sub-4',
      providerId: 'google',
      userId,
      accessToken: 'some-token',
      scope:
        'openid https://www.googleapis.com/auth/calendar.readonly https://www.googleapis.com/auth/calendar.events',
      createdAt: now,
      updatedAt: now,
    })

    await expect(getGoogleCalendarStatus(userId)).resolves.toEqual({ connected: true })
  })

  it('falls back to a token probe (and degrades to not-connected) when the scope column is unavailable', async () => {
    const db = createDb(env.DB)
    const { id: userId } = await makeUser(db)
    const now = new Date()
    await db.insert(account).values({
      id: 'acct_5',
      issuer: 'google',
      accountId: 'google-sub-5',
      providerId: 'google',
      userId,
      accessToken: 'some-token',
      // No `scope` recorded at all (older account row / provider that didn't report it) —
      // status must fall back to a token/freebusy probe rather than trusting token presence.
      // The test env has no GOOGLE_CLIENT_ID configured, so the token fetch itself degrades to
      // null and the probe never runs — this is the same "Google not configured" degrade as
      // `getGoogleAccessToken` above.
      createdAt: now,
      updatedAt: now,
    })

    await expect(getGoogleCalendarStatus(userId)).resolves.toEqual({ connected: false })
  })
})
