import { setupNetwork } from '@msw/cloudflare'
import { http, HttpResponse } from 'msw'
import { env } from 'cloudflare:workers'
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { createDb } from '#/server/db/client'
import { bookings } from '#/server/db/schema'
import { eq } from 'drizzle-orm'
import {
  syncGoogleEventCreate,
  syncGoogleEventDelete,
  syncGoogleEventsForReschedule,
} from '#/server/bookings/google-sync'
import { makeBooking, makeBookingPage, makeUser } from '../../../../test/helpers'

const testEnv = {
  EMAIL_FROM: 'whenweall <no-reply@whenweall.test>',
  APP_URL: 'https://whenweall.test',
  APP_ENV: 'test',
}

// `getGoogleAccessToken` always resolves `null` in this test environment (no GOOGLE_CLIENT_ID
// configured — see the "degrades to null" test in google/__tests__/calendar.workers.test.ts), so
// the Google-failure branches below can't be reached through the real thing. `google-sync.ts` runs
// as an ordinary worker module (not inside a Durable Object's separate `importModule()` path —
// see the note in booking-room.workers.test.ts for why that path can't be `vi.mock`ed), so mocking
// just this one export here is safe; `createCalendarClient` keeps its real implementation so msw
// still intercepts the actual Google HTTP calls.
vi.mock('#/server/google/calendar', async (importOriginal) => {
  const actual = await importOriginal<typeof import('#/server/google/calendar')>()
  return { ...actual, getGoogleAccessToken: vi.fn().mockResolvedValue('tok') }
})

const network = setupNetwork()

beforeAll(() => network.enable())
afterEach(() => network.resetHandlers())
afterAll(() => network.disable())

const EVENTS_URL = 'https://www.googleapis.com/calendar/v3/calendars/primary/events'

async function googleEventIdOf(db: ReturnType<typeof createDb>, bookingId: string) {
  const row = await db.query.bookings.findFirst({ where: eq(bookings.id, bookingId) })
  return row?.googleEventId ?? null
}

describe('syncGoogleEventCreate', () => {
  it('does nothing when the page has googleSync off', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, { googleSync: false })
    const page = await db.query.bookingPages.findFirst({
      where: (p, { eq: eqOp }) => eqOp(p.id, pageId),
    })
    const { id: bookingId } = await makeBooking(db, pageId, new Date().toISOString())

    const mailer = vi.fn().mockResolvedValue(true)
    await syncGoogleEventCreate(
      testEnv,
      db,
      page!,
      bookingId,
      {
        startAt: new Date().toISOString(),
        endAt: new Date().toISOString(),
        attendeeEmail: 'b@x.com',
      },
      fetch,
      mailer,
    )

    expect(await googleEventIdOf(db, bookingId)).toBeNull()
    expect(mailer).not.toHaveBeenCalled()
  })

  it('stores the created event id when googleSync is on and the call succeeds', async () => {
    network.use(
      http.post(EVENTS_URL, () => HttpResponse.json({ id: 'evt_created' }, { status: 200 })),
    )
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, { googleSync: true })
    const page = await db.query.bookingPages.findFirst({
      where: (p, { eq: eqOp }) => eqOp(p.id, pageId),
    })
    const { id: bookingId } = await makeBooking(db, pageId, new Date().toISOString())

    const mailer = vi.fn().mockResolvedValue(true)
    await syncGoogleEventCreate(
      testEnv,
      db,
      page!,
      bookingId,
      {
        startAt: new Date().toISOString(),
        endAt: new Date().toISOString(),
        attendeeEmail: 'b@x.com',
      },
      fetch,
      mailer,
    )

    expect(await googleEventIdOf(db, bookingId)).toBe('evt_created')
    expect(mailer).not.toHaveBeenCalled()
  })

  it('sends a best-effort organiser notice (once) when the create call fails', async () => {
    network.use(http.post(EVENTS_URL, () => HttpResponse.json({}, { status: 500 })))
    const db = createDb(env.DB)
    const { id: ownerId, email: ownerEmail } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, {
      googleSync: true,
      title: 'Sync test page',
    })
    const page = await db.query.bookingPages.findFirst({
      where: (p, { eq: eqOp }) => eqOp(p.id, pageId),
    })
    const { id: bookingId } = await makeBooking(db, pageId, new Date().toISOString())

    const mailer = vi.fn().mockResolvedValue(true)
    await syncGoogleEventCreate(
      testEnv,
      db,
      page!,
      bookingId,
      {
        startAt: new Date().toISOString(),
        endAt: new Date().toISOString(),
        attendeeEmail: 'b@x.com',
      },
      fetch,
      mailer,
    )

    expect(await googleEventIdOf(db, bookingId)).toBeNull()
    expect(mailer).toHaveBeenCalledTimes(1)
    const [, message] = mailer.mock.calls[0]!
    expect(message.to).toBe(ownerEmail)
    expect(message.subject).toContain('Sync test page')
  })
})

describe('syncGoogleEventDelete', () => {
  it('clears googleEventId to null when the delete succeeds', async () => {
    network.use(http.delete(`${EVENTS_URL}/evt_1`, () => new HttpResponse(null, { status: 204 })))
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    // googleSync is now off (simulating the page setting having changed since the event was
    // created) — the delete must still run (finding 7).
    const { id: pageId } = await makeBookingPage(db, ownerId, { googleSync: false })
    const page = await db.query.bookingPages.findFirst({
      where: (p, { eq: eqOp }) => eqOp(p.id, pageId),
    })
    const { id: bookingId } = await makeBooking(db, pageId, new Date().toISOString(), {
      googleEventId: 'evt_1',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    await syncGoogleEventDelete(testEnv, db, page!, bookingId, 'evt_1', fetch, mailer)

    expect(await googleEventIdOf(db, bookingId)).toBeNull()
    expect(mailer).not.toHaveBeenCalled()
  })

  it('sends a best-effort organiser notice (once) and leaves googleEventId set when the delete fails', async () => {
    network.use(http.delete(`${EVENTS_URL}/evt_2`, () => HttpResponse.json({}, { status: 500 })))
    const db = createDb(env.DB)
    const { id: ownerId, email: ownerEmail } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, { title: 'Delete-fail page' })
    const page = await db.query.bookingPages.findFirst({
      where: (p, { eq: eqOp }) => eqOp(p.id, pageId),
    })
    const { id: bookingId } = await makeBooking(db, pageId, new Date().toISOString(), {
      googleEventId: 'evt_2',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    await syncGoogleEventDelete(testEnv, db, page!, bookingId, 'evt_2', fetch, mailer)

    expect(await googleEventIdOf(db, bookingId)).toBe('evt_2')
    expect(mailer).toHaveBeenCalledTimes(1)
    const [, message] = mailer.mock.calls[0]!
    expect(message.to).toBe(ownerEmail)
    expect(message.subject).toContain('Delete-fail page')
  })
})

describe('syncGoogleEventsForReschedule', () => {
  it('does not create a new event when the delete fails, and leaves googleEventId unchanged', async () => {
    network.use(http.delete(`${EVENTS_URL}/evt_old`, () => HttpResponse.json({}, { status: 500 })))
    const createHandler = vi.fn(() => HttpResponse.json({ id: 'evt_new' }, { status: 200 }))
    network.use(http.post(EVENTS_URL, createHandler))

    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, { googleSync: true })
    const page = await db.query.bookingPages.findFirst({
      where: (p, { eq: eqOp }) => eqOp(p.id, pageId),
    })
    const { id: bookingId } = await makeBooking(db, pageId, new Date().toISOString(), {
      googleEventId: 'evt_old',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    await syncGoogleEventsForReschedule(
      testEnv,
      db,
      page!,
      bookingId,
      'evt_old',
      {
        startAt: new Date().toISOString(),
        endAt: new Date().toISOString(),
        attendeeEmail: 'b@x.com',
      },
      fetch,
      mailer,
    )

    expect(createHandler).not.toHaveBeenCalled()
    expect(await googleEventIdOf(db, bookingId)).toBe('evt_old')
    expect(mailer).toHaveBeenCalledTimes(1)
  })

  it('creates the new event and stores its id when the delete finds the old one already gone (404)', async () => {
    network.use(http.delete(`${EVENTS_URL}/evt_gone`, () => HttpResponse.json({}, { status: 404 })))
    network.use(http.post(EVENTS_URL, () => HttpResponse.json({ id: 'evt_new' }, { status: 200 })))

    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pageId } = await makeBookingPage(db, ownerId, { googleSync: true })
    const page = await db.query.bookingPages.findFirst({
      where: (p, { eq: eqOp }) => eqOp(p.id, pageId),
    })
    const { id: bookingId } = await makeBooking(db, pageId, new Date().toISOString(), {
      googleEventId: 'evt_gone',
    })

    const mailer = vi.fn().mockResolvedValue(true)
    await syncGoogleEventsForReschedule(
      testEnv,
      db,
      page!,
      bookingId,
      'evt_gone',
      {
        startAt: new Date().toISOString(),
        endAt: new Date().toISOString(),
        attendeeEmail: 'b@x.com',
      },
      fetch,
      mailer,
    )

    expect(await googleEventIdOf(db, bookingId)).toBe('evt_new')
    expect(mailer).not.toHaveBeenCalled()
  })
})
