import { DurableObject } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import type { DigestEvent } from '#/lib/notifications'
import { createDb } from '#/server/db/client'
import { pollOptions, polls } from '#/server/db/schema'
import { sendMail } from '#/server/mailer/mailer'
// Imported lazily at the send site below: the templates module pulls in React,
// @react-email/render and every email component. In workerd that is a lazy instantiation of an
// already-bundled module; what it buys is keeping that graph out of the DO bundle (and out of
// every `*.workers.test.ts` isolate, which all load this worker entry).
import type { DigestLine } from '#/server/mailer/templates'
import { emitPollEvent } from '#/server/notifications/emit'
import { resolveRecipients, type Recipient } from '#/server/notifications/recipients'
import { applyClaim, countClaims, removeClaim, type ClaimIdentity } from '#/server/polls/claims'
import { closeExpiredPoll } from '#/server/polls/service'
import type { DigestItem, PollEvent } from './protocol'

export const DIGEST_DELAY_MS = 10 * 60_000
export const RETRY_DELAY_MS = 5 * 60_000
export const MAX_RETRIES = 3
/** How far ahead of a poll's deadline the "closes soon" reminder fires. */
export const REMINDER_LEAD_MS = 24 * 60 * 60_000

const POLL_ID_KEY = 'pollId'
const DIGEST_ITEMS_KEY = 'digest:items'
const DIGEST_AT_KEY = 'digest:at'
const DIGEST_SENT_KEY = 'digest:sent'
const DEADLINE_AT_KEY = 'deadline:at'
const REMIND_AT_KEY = 'remind:at'
const RETRY_COUNT_KEY = 'retry:count'
const SIGNUP_FULL_KEY = 'signup:full'

/** Collapses a recipient's queued items into one summarised row per event, preserving the order
 * the events first appeared so the mail reads chronologically. Names are deduped — the same
 * person editing twice inside one window is one name, not two. */
function buildDigestLines(items: DigestItem[]): DigestLine[] {
  const byEvent = new Map<DigestEvent, { names: Set<string>; count: number }>()
  for (const item of items) {
    const line = byEvent.get(item.event) ?? { names: new Set<string>(), count: 0 }
    line.count += 1
    if (item.name) line.names.add(item.name)
    byEvent.set(item.event, line)
  }
  return [...byEvent.entries()].map(([event, line]) => ({
    event,
    names: [...line.names],
    count: line.count,
  }))
}

/**
 * One PollRoom durable object per poll (keyed by pollId). Fans out live changes to connected
 * WebSocket clients and owns two alarms: a debounced "digest" mail to the owner (batches votes
 * and comments), and a "deadline" close-and-notify once the poll's deadline passes.
 */
export class PollRoom extends DurableObject<Env> {
  /**
   * Overridable seam for tests. `vi.mock` cannot reach code running inside a Durable Object's own
   * module graph in @cloudflare/vitest-plugin 1.0 — the DO's `alarm()`/RPC methods are loaded via
   * a separate `importModule()` path that does not share the test file's mock registry. Tests
   * instead override this instance field directly via `runInDurableObject` before invoking the
   * alarm. Production code never touches this — it always calls the real `sendMail`.
   */
  mailer: typeof sendMail = sendMail

  /**
   * Tail of an in-memory promise chain used to serialise `claim`/`unclaim` calls. A DO's "input
   * gate" only guarantees ordering around `ctx.storage` operations — `claim`/`unclaim` instead
   * write to D1 (an external service, awaited like any `fetch`), so two RPC calls that arrive
   * back-to-back can otherwise interleave their reads/writes and both see the same pre-write
   * capacity count (confirmed by a failing concurrency test before this field was added). Chaining
   * every call onto this tail — synchronously, before either call's first `await` — forces them to
   * run one at a time in arrival order, which is exactly what makes the capacity check atomic.
   */
  #claimQueue: Promise<unknown> = Promise.resolve()

  #serialize<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.#claimQueue.then(fn, fn)
    // Keep the chain alive regardless of whether `fn` resolved or rejected — only the caller of
    // this particular call should see its outcome; the next queued call must still proceed.
    this.#claimQueue = run.then(
      () => undefined,
      () => undefined,
    )
    return run
  }

  async fetch(request: Request): Promise<Response> {
    if (request.headers.get('Upgrade') !== 'websocket') {
      return new Response('Expected Upgrade: websocket', { status: 426 })
    }

    const url = new URL(request.url)
    const pollId = url.searchParams.get('pollId')
    if (pollId) {
      await this.#setPollId(pollId)
    }

    const { 0: client, 1: server } = new WebSocketPair()
    this.ctx.acceptWebSocket(server)
    server.serializeAttachment({ connectedAt: Date.now() })
    await this.#broadcastPresence()

    return new Response(null, { status: 101, webSocket: client })
  }

  async broadcast(pollId: string, event: PollEvent): Promise<number> {
    await this.#setPollId(pollId)
    return this.#send(event)
  }

  async enqueueDigest(pollId: string, item: DigestItem): Promise<void> {
    await this.#setPollId(pollId)

    const items = (await this.ctx.storage.get<DigestItem[]>(DIGEST_ITEMS_KEY)) ?? []
    items.push(item)
    await this.ctx.storage.put(DIGEST_ITEMS_KEY, items)

    const digestAt = await this.ctx.storage.get<number>(DIGEST_AT_KEY)
    if (digestAt === undefined) {
      await this.ctx.storage.put(DIGEST_AT_KEY, Date.now() + DIGEST_DELAY_MS)
    }

    await this.#rearm()
  }

  /** Test seam: lets a workers test assert what was enqueued without reaching into DO storage. */
  async peekDigestItems(): Promise<DigestItem[]> {
    return (await this.ctx.storage.get<DigestItem[]>(DIGEST_ITEMS_KEY)) ?? []
  }

  /**
   * Runs the claims service against this poll's own D1 rows from inside the DO — one poll's
   * writes are handled by exactly one DO instance, so this call is serialised with every other
   * claim/unclaim for the same `pollId` (no separate lock needed to make capacity checks atomic).
   * Auth (owner/session/edit token) is the caller's job; this trusts whatever identity it's given.
   */
  claim(
    pollId: string,
    optionId: string,
    identity: ClaimIdentity,
  ): Promise<Awaited<ReturnType<typeof applyClaim>>> {
    return this.#serialize(async () => {
      await this.#setPollId(pollId)
      const db = createDb(this.env.DB)
      const result = await applyClaim(db, pollId, optionId, identity)
      // A re-claim of a slot the participant already holds is a no-op (`changed: false`) — nothing
      // for anyone else to see, so skip the broadcast rather than fan out a phantom update.
      if (result.changed) {
        this.#send({ type: 'poll.changed', entity: 'vote' })
        // Inside `#serialize`, so the capacity read is atomic with the claim that may have just
        // filled the sheet — outside it, two concurrent claims could both miss the transition or
        // both announce it.
        await this.#emitIfSheetFilled(db, pollId)
      }
      return result
    })
  }

  /**
   * Fires `signup.full` on the transition to "every slot taken". Options with no capacity are
   * unlimited, so a sheet containing one can never be full.
   */
  async #emitIfSheetFilled(db: ReturnType<typeof createDb>, pollId: string): Promise<void> {
    try {
      const options = await db.query.pollOptions.findMany({
        where: eq(pollOptions.pollId, pollId),
      })
      if (options.length === 0) return
      if (options.some((option) => option.capacity === null)) return

      const counts = await countClaims(db, pollId)
      const full = options.every((option) => (counts[option.id] ?? 0) >= option.capacity!)
      if (!full) return

      // Storage-flagged so a later claim/unclaim cycle on an already-full sheet does not
      // re-announce it every time someone re-takes the last slot.
      if (await this.ctx.storage.get<boolean>(SIGNUP_FULL_KEY)) return
      await this.ctx.storage.put(SIGNUP_FULL_KEY, true)

      await emitPollEvent(pollId, 'signup.full', { actorUserId: null }, { mailer: this.mailer })
    } catch (err) {
      console.error('[PollRoom] signup.full check failed', err)
    }
  }

  unclaim(
    pollId: string,
    optionId: string,
    participantId: string,
    opts: { allowClosed?: boolean } = {},
  ): Promise<{ remainingOptionIds: string[] }> {
    return this.#serialize(async () => {
      await this.#setPollId(pollId)
      const db = createDb(this.env.DB)
      const result = await removeClaim(db, pollId, optionId, participantId, opts)
      this.#send({ type: 'poll.changed', entity: 'vote' })
      // Freeing a slot re-arms the announcement, so filling the sheet again is news again.
      await this.ctx.storage.delete(SIGNUP_FULL_KEY)
      return result
    })
  }

  async syncDeadline(pollId: string, deadlineAt: string | null): Promise<void> {
    await this.#setPollId(pollId)

    if (deadlineAt === null) {
      await this.ctx.storage.delete(DEADLINE_AT_KEY)
      await this.ctx.storage.delete(REMIND_AT_KEY)
    } else {
      const at = new Date(deadlineAt).getTime()
      await this.ctx.storage.put(DEADLINE_AT_KEY, at)

      // Only arm the reminder when it is still ahead of us. A poll created with a deadline
      // already inside the next 24 hours would otherwise fire "closes soon" immediately, which
      // reads as a bug rather than a reminder.
      const remindAt = at - REMINDER_LEAD_MS
      if (remindAt > Date.now()) await this.ctx.storage.put(REMIND_AT_KEY, remindAt)
      else await this.ctx.storage.delete(REMIND_AT_KEY)
    }

    await this.#rearm()
  }

  async alarm(): Promise<void> {
    const pollId = await this.ctx.storage.get<string>(POLL_ID_KEY)
    if (!pollId) {
      await this.#rearm()
      return
    }

    const now = Date.now()
    const db = createDb(this.env.DB)

    try {
      // Each branch is isolated: a failure in one must not block the other, and both must not
      // prevent re-arming (see the `finally` below) — otherwise a stuck poll could silently stop
      // getting alarm ticks at all.
      try {
        const deadlineAt = await this.ctx.storage.get<number>(DEADLINE_AT_KEY)
        if (deadlineAt !== undefined && deadlineAt <= now) {
          await this.#processDeadline(db, pollId)
        }
      } catch (err) {
        console.error('[PollRoom] deadline step failed', err)
      }

      try {
        const remindAt = await this.ctx.storage.get<number>(REMIND_AT_KEY)
        if (remindAt !== undefined && remindAt <= now) {
          await this.#processReminder(pollId)
        }
      } catch (err) {
        console.error('[PollRoom] reminder step failed', err)
      }

      try {
        const digestAt = await this.ctx.storage.get<number>(DIGEST_AT_KEY)
        if (digestAt !== undefined && digestAt <= now) {
          await this.#processDigest(db, pollId, now)
        }
      } catch (err) {
        console.error('[PollRoom] digest step failed', err)
      }
    } finally {
      await this.#rearm()
    }
  }

  webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): void {
    if (message === 'ping') {
      try {
        ws.send('pong')
      } catch {
        // Socket already gone; nothing to do.
      }
    }
  }

  webSocketClose(ws: WebSocket, code: number, reason: string, _wasClean: boolean): void {
    try {
      ws.close(code, reason)
    } catch {
      // Already closed/closing — ignore.
    }
    // `ws` can still be present in `ctx.getWebSockets()` at this point, so exclude it explicitly —
    // otherwise the closing socket's own count is briefly broadcast to everyone else too.
    void this.#broadcastPresence(ws)
  }

  webSocketError(ws: WebSocket, _error: unknown): void {
    try {
      ws.close()
    } catch {
      // Already closed/closing — ignore.
    }
    void this.#broadcastPresence()
  }

  async #setPollId(pollId: string): Promise<void> {
    await this.ctx.storage.put(POLL_ID_KEY, pollId)
  }

  #send(event: PollEvent): number {
    const payload = JSON.stringify(event)
    let count = 0
    for (const ws of this.ctx.getWebSockets()) {
      try {
        ws.send(payload)
        count += 1
      } catch {
        // Drop sockets that can't be sent to.
      }
    }
    return count
  }

  async #broadcastPresence(exclude?: WebSocket): Promise<void> {
    const count = exclude
      ? this.ctx.getWebSockets().filter((s) => s !== exclude).length
      : this.ctx.getWebSockets().length
    this.#send({ type: 'presence', count })
  }

  async #processDeadline(db: ReturnType<typeof createDb>, pollId: string): Promise<void> {
    const changed = await closeExpiredPoll(db, pollId)

    if (changed) {
      // Broadcast first: connected clients should see the poll flip to closed even if the
      // notification below fails.
      this.#send({ type: 'poll.changed', entity: 'poll' })

      // Best-effort: the poll is already closed, so a failure here must not stop us from deleting
      // `deadline:at` below (there's nothing to retry — the close already happened). `emitPollEvent`
      // catches internally, but the deadline is system-driven so there is no actor to suppress.
      // `this.mailer` is threaded through so the DO's test seam still covers this send.
      await emitPollEvent(pollId, 'poll.closed', { actorUserId: null }, { mailer: this.mailer })
    }

    await this.ctx.storage.delete(DEADLINE_AT_KEY)
  }

  /** Fires once, 24 hours before the deadline, for everyone subscribed to `deadline.approaching`. */
  async #processReminder(pollId: string): Promise<void> {
    await emitPollEvent(
      pollId,
      'deadline.approaching',
      { actorUserId: null },
      {
        mailer: this.mailer,
      },
    )
    await this.ctx.storage.delete(REMIND_AT_KEY)
  }

  /**
   * Sends one digest per subscribed recipient.
   *
   * Recipients and their preferences are resolved here rather than at enqueue time, so a toggle
   * flipped during the ten-minute debounce window still takes effect. Each item is suppressed for
   * the person who caused it.
   */
  async #processDigest(
    db: ReturnType<typeof createDb>,
    pollId: string,
    now: number,
  ): Promise<void> {
    const items = (await this.ctx.storage.get<DigestItem[]>(DIGEST_ITEMS_KEY)) ?? []
    const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })

    if (!poll || poll.deletedAt || items.length === 0) {
      await this.#clearDigest()
      return
    }

    const scope = {
      type: 'poll' as const,
      id: pollId,
      organizationId: poll.organizationId,
    }

    // One resolution per distinct event, not per item — a burst of twenty votes resolves once.
    const perEvent = new Map<DigestEvent, Recipient[]>()
    for (const event of new Set(items.map((i) => i.event))) {
      perEvent.set(event, (await resolveRecipients(db, scope, event)).email)
    }

    // Invert into "what does each recipient get", dropping items they caused themselves.
    const byRecipient = new Map<string, { recipient: Recipient; items: DigestItem[] }>()
    for (const item of items) {
      for (const recipient of perEvent.get(item.event) ?? []) {
        if (recipient.userId === item.actorUserId) continue
        const bucket = byRecipient.get(recipient.userId) ?? { recipient, items: [] }
        bucket.items.push(item)
        byRecipient.set(recipient.userId, bucket)
      }
    }

    // Recipients already mailed on an earlier attempt are skipped, so a retry triggered by one
    // failing address cannot deliver a second copy to everyone who already succeeded.
    const delivered = new Set((await this.ctx.storage.get<string[]>(DIGEST_SENT_KEY)) ?? [])
    let anyFailed = false

    for (const { recipient, items: theirs } of byRecipient.values()) {
      if (delivered.has(recipient.userId)) continue

      try {
        const { renderDigest } = await import('#/server/mailer/templates')
        const rendered = await renderDigest({
          pollTitle: poll.title,
          pollUrl: `${this.env.APP_URL}/p/${pollId}`,
          lines: buildDigestLines(theirs),
          locale: recipient.locale,
        })
        const ok = await this.mailer(this.env, { to: recipient.email, ...rendered })
        if (ok) delivered.add(recipient.userId)
        else anyFailed = true
      } catch (err) {
        console.error('[PollRoom] digest mail threw', err)
        anyFailed = true
      }
    }

    if (anyFailed) {
      const currentRetries = (await this.ctx.storage.get<number>(RETRY_COUNT_KEY)) ?? 0
      const nextRetries = currentRetries + 1
      if (nextRetries < MAX_RETRIES) {
        await this.ctx.storage.put(RETRY_COUNT_KEY, nextRetries)
        await this.ctx.storage.put(DIGEST_AT_KEY, now + RETRY_DELAY_MS)
        await this.ctx.storage.put(DIGEST_SENT_KEY, [...delivered])
        return
      }
      console.error(
        `PollRoom: dropping digest for poll ${pollId} after ${MAX_RETRIES} failed mail attempts`,
      )
    }

    await this.#clearDigest()
  }

  async #clearDigest(): Promise<void> {
    await this.ctx.storage.delete(DIGEST_ITEMS_KEY)
    await this.ctx.storage.delete(DIGEST_AT_KEY)
    await this.ctx.storage.delete(RETRY_COUNT_KEY)
    await this.ctx.storage.delete(DIGEST_SENT_KEY)
  }

  async #rearm(): Promise<void> {
    const digestAt = await this.ctx.storage.get<number>(DIGEST_AT_KEY)
    const deadlineAt = await this.ctx.storage.get<number>(DEADLINE_AT_KEY)
    const remindAt = await this.ctx.storage.get<number>(REMIND_AT_KEY)
    const candidates = [digestAt, deadlineAt, remindAt].filter((v): v is number => v !== undefined)

    if (candidates.length > 0) {
      await this.ctx.storage.setAlarm(Math.min(...candidates))
    } else {
      await this.ctx.storage.deleteAlarm()
    }
  }
}
