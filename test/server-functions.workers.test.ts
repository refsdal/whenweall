import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { requireSessionMiddleware, sessionMiddleware } from '#/server/auth/middleware'
import { rateLimitMiddleware } from '#/server/http/rate-limit.middleware'
import { resolveVerifiedParticipantId } from '#/server/polls/comment-auth'
import { addParticipant } from '#/server/polls/participants'
import * as pollsFunctions from '#/server/polls/polls.functions'
import {
  resolveAuthorName,
  SERVER_FN_MIDDLEWARE as PARTICIPANTS_MIDDLEWARE,
} from '#/server/polls/participants.functions'
import * as participantsFunctions from '#/server/polls/participants.functions'
import * as pagesFunctions from '#/server/bookings/pages.functions'
import * as bookingsFunctions from '#/server/bookings/bookings.functions'
import { makePoll, makeUser } from './helpers'

// Server functions pull in `cloudflare:workers`, better-auth, and rate-limit/turnstile modules
// that only resolve correctly inside the Workers runtime. This proves the module graph for both
// function files loads and evaluates cleanly in workerd (no top-level throw, no unresolved
// import) — a lightweight substitute for exercising them over the TanStack Start RPC transport,
// which is awkward to hand-construct outside a real client fetcher.
describe('polls.functions module graph', () => {
  it.each([
    'getPoll',
    'createPoll',
    'updatePoll',
    'setPollStatus',
    'finalizePoll',
    'deletePoll',
    'duplicatePoll',
    'listMyPolls',
    'updateNotificationPrefs',
  ] as const)('exports a callable %s server function', (name) => {
    expect(typeof pollsFunctions[name]).toBe('function')
  })
})

describe('participants.functions module graph', () => {
  it.each([
    'addParticipant',
    'updateParticipant',
    'removeParticipant',
    'addComment',
    'deleteComment',
    'claimSlot',
    'unclaimSlot',
  ] as const)('exports a callable %s server function', (name) => {
    expect(typeof participantsFunctions[name]).toBe('function')
  })
})

/*
 * A built `createServerFn(...)` object does not expose its `.middleware([...])` array at runtime:
 * the only own properties on the returned function are `method` and `__executeServer` (confirmed
 * by inspecting `Object.getOwnPropertyNames` on one of these functions directly — there is no
 * `.options`, no symbol, nothing to introspect). Calling the function itself would require
 * hand-constructing a TanStack Start RPC request, which is what this file's module-graph tests
 * above already note is awkward outside a real client fetcher.
 *
 * Instead, each functions file exports a `SERVER_FN_MIDDLEWARE` manifest built from the *same*
 * array references passed to each `.middleware(...)` call (see the top of polls.functions.ts and
 * participants.functions.ts) — so asserting against the manifest is equivalent to asserting
 * against what each function actually runs, with no risk of the manifest drifting out of sync.
 */
describe('polls.functions middleware wiring', () => {
  const M = pollsFunctions.SERVER_FN_MIDDLEWARE

  it.each([
    'createPoll',
    'updatePoll',
    'setPollStatus',
    'finalizePoll',
    'deletePoll',
    'duplicatePoll',
    'listMyPolls',
    'updateNotificationPrefs',
  ] as const)('%s requires a session via requireSessionMiddleware', (name) => {
    expect(M[name]).toContain(requireSessionMiddleware)
  })

  it('getPoll only requires the (optional) session lookup, not requireSessionMiddleware', () => {
    expect(M.getPoll).toContain(sessionMiddleware)
    expect(M.getPoll).not.toContain(requireSessionMiddleware)
  })

  it('createPoll is also rate-limited on the create action', () => {
    expect(M.createPoll).toContain(rateLimitMiddleware('create'))
  })
})

describe('participants.functions middleware wiring', () => {
  const M = PARTICIPANTS_MIDDLEWARE

  it('addParticipant requires a session lookup and the vote rate limit', () => {
    expect(M.addParticipant).toContain(sessionMiddleware)
    expect(M.addParticipant).toContain(rateLimitMiddleware('vote'))
  })

  it('updateParticipant and removeParticipant are both vote-rate-limited', () => {
    expect(M.updateParticipant).toContain(rateLimitMiddleware('vote'))
    expect(M.removeParticipant).toContain(rateLimitMiddleware('vote'))
  })

  it('addComment is rate-limited on the comment action, not vote', () => {
    expect(M.addComment).toContain(rateLimitMiddleware('comment'))
    expect(M.addComment).not.toContain(rateLimitMiddleware('vote'))
  })

  it('deleteComment only requires a session lookup', () => {
    expect(M.deleteComment).toContain(sessionMiddleware)
    expect(M.deleteComment).toHaveLength(1)
  })

  it('claimSlot and unclaimSlot are both vote-rate-limited', () => {
    expect(M.claimSlot).toContain(sessionMiddleware)
    expect(M.claimSlot).toContain(rateLimitMiddleware('vote'))
    expect(M.unclaimSlot).toContain(sessionMiddleware)
    expect(M.unclaimSlot).toContain(rateLimitMiddleware('vote'))
  })
})

describe('pages.functions module graph', () => {
  it.each([
    'createBookingPage',
    'updateBookingPage',
    'deleteBookingPage',
    'listMyBookingPages',
    'getBookingPage',
    'setHandle',
    'getGoogleCalendarStatus',
    'disconnectGoogleCalendar',
  ] as const)('exports a callable %s server function', (name) => {
    expect(typeof pagesFunctions[name]).toBe('function')
  })
})

describe('bookings.functions module graph', () => {
  it.each([
    'getPublicAvailability',
    'bookSlot',
    'getManagedBooking',
    'cancelBooking',
    'rescheduleBooking',
    'listPageBookings',
  ] as const)('exports a callable %s server function', (name) => {
    expect(typeof bookingsFunctions[name]).toBe('function')
  })
})

describe('pages.functions middleware wiring', () => {
  const M = pagesFunctions.SERVER_FN_MIDDLEWARE

  it.each([
    'createBookingPage',
    'updateBookingPage',
    'deleteBookingPage',
    'listMyBookingPages',
    'getBookingPage',
    'setHandle',
    'getGoogleCalendarStatus',
    'disconnectGoogleCalendar',
  ] as const)('%s requires a session via requireSessionMiddleware', (name) => {
    expect(M[name]).toContain(requireSessionMiddleware)
  })
})

describe('bookings.functions middleware wiring', () => {
  const M = bookingsFunctions.SERVER_FN_MIDDLEWARE

  it('getPublicAvailability has no auth middleware (it is a public lookup) but is rate-limited', () => {
    expect(M.getPublicAvailability).not.toContain(sessionMiddleware)
    expect(M.getPublicAvailability).not.toContain(requireSessionMiddleware)
    expect(M.getPublicAvailability).toContain(rateLimitMiddleware('book'))
  })

  it('bookSlot requires a session lookup and is rate-limited on the book action', () => {
    expect(M.bookSlot).toContain(sessionMiddleware)
    expect(M.bookSlot).toContain(rateLimitMiddleware('book'))
    expect(M.bookSlot).not.toContain(requireSessionMiddleware)
  })

  it('getManagedBooking only needs the optional session lookup — the manage token is the other auth path', () => {
    expect(M.getManagedBooking).toContain(sessionMiddleware)
    expect(M.getManagedBooking).not.toContain(requireSessionMiddleware)
    expect(M.getManagedBooking).not.toContain(rateLimitMiddleware('book'))
  })

  it('cancelBooking and rescheduleBooking need the optional session lookup and are rate-limited on the book action', () => {
    for (const name of ['cancelBooking', 'rescheduleBooking'] as const) {
      expect(M[name]).toContain(sessionMiddleware)
      expect(M[name]).not.toContain(requireSessionMiddleware)
      expect(M[name]).toContain(rateLimitMiddleware('book'))
    }
  })

  it('listPageBookings requires a session (owner-only)', () => {
    expect(M.listPageBookings).toContain(requireSessionMiddleware)
  })
})

describe('resolveVerifiedParticipantId (addComment participantId-verification branch)', () => {
  it('resolves the participant id when the edit token matches', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const { participantId, editToken } = await addParticipant(db, pollId, {
      name: 'Alice',
      answers: {},
      userId: null,
    })

    await expect(resolveVerifiedParticipantId(db, pollId, participantId, editToken)).resolves.toBe(
      participantId,
    )
  })

  it('returns null for a wrong edit token', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const { participantId } = await addParticipant(db, pollId, {
      name: 'Alice',
      answers: {},
      userId: null,
    })

    await expect(
      resolveVerifiedParticipantId(db, pollId, participantId, 'wrong-token'),
    ).resolves.toBeNull()
  })

  it('returns null when the participant belongs to a different poll', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const { id: otherPollId } = await makePoll(db, ownerId)
    const { participantId, editToken } = await addParticipant(db, otherPollId, {
      name: 'Alice',
      answers: {},
      userId: null,
    })

    await expect(
      resolveVerifiedParticipantId(db, pollId, participantId, editToken),
    ).resolves.toBeNull()
  })

  it('returns null when either the participantId or editToken is missing', async () => {
    const db = createDb(env.DB)
    await expect(resolveVerifiedParticipantId(db, 'poll12345678', null, 'tok')).resolves.toBeNull()
    await expect(resolveVerifiedParticipantId(db, 'poll12345678', 'pa_x', null)).resolves.toBeNull()
  })
})

describe('resolveAuthorName (addComment authorName-from-session branch)', () => {
  it('uses the session name for a signed-in author, ignoring the client value', () => {
    expect(resolveAuthorName({ user: { name: 'Ada' } }, 'Someone Else')).toBe('Ada')
  })

  it('falls back to the client-supplied name for a guest (no session)', () => {
    expect(resolveAuthorName(null, 'Guest Name')).toBe('Guest Name')
    expect(resolveAuthorName(undefined, 'Guest Name')).toBe('Guest Name')
  })
})
