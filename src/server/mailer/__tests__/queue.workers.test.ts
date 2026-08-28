import { env } from 'cloudflare:workers'
import { describe, expect, it, vi } from 'vitest'
import { createDb } from '#/server/db/client'
import { enqueueMailRetry, processMailJob, type MailJob } from '#/server/mailer/queue'
import { makeBookingPage, makeUserWithOrg } from '../../../../test/helpers'
import { bookings } from '#/server/db/schema'
import { newId } from '#/lib/ids'

function stubQueue() {
  const sent: MailJob[] = []
  return { sent, queue: { send: vi.fn(async (j: MailJob) => void sent.push(j)) } }
}

describe('enqueueMailRetry', () => {
  it('sends the job when a queue is bound', async () => {
    const { sent, queue } = stubQueue()

    await enqueueMailRetry(
      { MAIL_QUEUE: queue as unknown as Queue<MailJob> },
      {
        kind: 'booking',
        event: 'confirmed',
        bookingId: 'b1',
      },
    )

    expect(sent).toEqual([{ kind: 'booking', event: 'confirmed', bookingId: 'b1' }])
  })

  it('carries ids only — never an address, a rendered body or a token', async () => {
    const { sent, queue } = stubQueue()

    await enqueueMailRetry(
      { MAIL_QUEUE: queue as unknown as Queue<MailJob> },
      {
        kind: 'booking',
        event: 'confirmed',
        bookingId: 'b1',
      },
    )

    expect(Object.keys(sent[0]!).sort()).toEqual(['bookingId', 'event', 'kind'])
  })

  it('does nothing when no queue is bound, rather than throwing', async () => {
    await expect(
      enqueueMailRetry({}, { kind: 'booking', event: 'reminder', bookingId: 'b1' }),
    ).resolves.toBeUndefined()
  })

  // This runs on the failure path. A queue outage must not turn "the confirmation did not send"
  // into "the booking request failed".
  it('swallows a queue failure so the caller is never broken by it', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const queue = { send: vi.fn().mockRejectedValue(new Error('queue down')) }

    await expect(
      enqueueMailRetry(
        { MAIL_QUEUE: queue as unknown as Queue<MailJob> },
        {
          kind: 'booking',
          event: 'confirmed',
          bookingId: 'b1',
        },
      ),
    ).resolves.toBeUndefined()
    vi.restoreAllMocks()
  })
})

describe('processMailJob', () => {
  // The trap this pins: acking a job whose subject is gone. Reporting `retry` here would burn
  // all five attempts and then park a misleading entry in the dead-letter queue.
  it('acknowledges a job whose booking no longer exists instead of retrying it', async () => {
    const db = createDb(env.DB)

    const outcome = await processMailJob(env, db, {
      kind: 'booking',
      event: 'reminder',
      bookingId: newId(),
    })

    expect(outcome).toBe('nothing-to-send')
  })

  it('reports sent once the mail goes out', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeUserWithOrg(db)
    const { id: pageId } = await makeBookingPage(db, { orgId, createdBy: userId })

    const bookingId = newId()
    const startAt = new Date(Date.now() + 86_400_000).toISOString()
    await db.insert(bookings).values({
      id: bookingId,
      pageId,
      startAt,
      endAt: new Date(Date.parse(startAt) + 1_800_000).toISOString(),
      visitorName: 'Ada',
      visitorEmail: 'ada@example.com',
      visitorTimezone: 'Europe/Oslo',
      visitorLocale: 'en',
      status: 'confirmed',
      manageTokenHash: 'x'.repeat(64),
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    })

    const outcome = await processMailJob(env, db, {
      kind: 'booking',
      event: 'reminder',
      bookingId,
    })

    expect(outcome).toBe('sent')
  })
})
