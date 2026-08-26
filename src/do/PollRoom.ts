import { DurableObject } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { createDb } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import { sendMail } from '#/server/mailer/mailer'
import { renderClosed, renderDigest } from '#/server/mailer/templates'
import { applyClaim, removeClaim, type ClaimIdentity } from '#/server/polls/claims'
import { closeExpiredPoll } from '#/server/polls/service'
import type { DigestItem, PollEvent } from './protocol'

export const DIGEST_DELAY_MS = 10 * 60_000
export const RETRY_DELAY_MS = 5 * 60_000
export const MAX_RETRIES = 3

const POLL_ID_KEY = 'pollId'
const DIGEST_ITEMS_KEY = 'digest:items'
const DIGEST_AT_KEY = 'digest:at'
const DEADLINE_AT_KEY = 'deadline:at'
const RETRY_COUNT_KEY = 'retry:count'

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
      if (result.changed) this.#send({ type: 'poll.changed', entity: 'vote' })
      return result
    })
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
      return result
    })
  }

  async syncDeadline(pollId: string, deadlineAt: string | null): Promise<void> {
    await this.#setPollId(pollId)

    if (deadlineAt === null) {
      await this.ctx.storage.delete(DEADLINE_AT_KEY)
    } else {
      await this.ctx.storage.put(DEADLINE_AT_KEY, new Date(deadlineAt).getTime())
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
      // owner-notification mail below fails.
      this.#send({ type: 'poll.changed', entity: 'poll' })

      // Best-effort notification: the poll is already closed, so a failure here must not stop us
      // from deleting `deadline:at` below (there's nothing to retry — the close already happened).
      try {
        const poll = await db.query.polls.findFirst({
          where: eq(polls.id, pollId),
          with: { owner: true },
        })
        // `owner` (poll.createdBy) is nullable — set to null if the creator's account was
        // deleted. There's no fallback recipient (e.g. mailing every org admin) in scope here;
        // the notification is simply skipped, same as any other best-effort mail failure.
        if (poll?.owner) {
          const rendered = await renderClosed({
            pollTitle: poll.title,
            pollUrl: `${this.env.APP_URL}/p/${pollId}`,
            locale: poll.owner.locale ?? 'en',
          })
          await this.mailer(this.env, { to: poll.owner.email, ...rendered })
        }
      } catch (err) {
        console.error('[PollRoom] deadline notification failed', err)
      }
    }

    await this.ctx.storage.delete(DEADLINE_AT_KEY)
  }

  async #processDigest(
    db: ReturnType<typeof createDb>,
    pollId: string,
    now: number,
  ): Promise<void> {
    const items = (await this.ctx.storage.get<DigestItem[]>(DIGEST_ITEMS_KEY)) ?? []
    const poll = await db.query.polls.findFirst({
      where: eq(polls.id, pollId),
      with: { owner: true },
    })

    const filtered = poll
      ? items.filter((item) => (item.kind === 'vote' ? poll.notifyOnVote : poll.notifyOnComment))
      : []

    // `owner` (poll.createdBy) is nullable — same graceful skip as `#processDeadline` above when
    // the creator's account is gone.
    if (!poll || poll.deletedAt || filtered.length === 0 || !poll.owner) {
      await this.#clearDigest()
      return
    }

    const newVoters = filtered.filter((item) => item.kind === 'vote').map((item) => item.name)
    const newComments = filtered.filter((item) => item.kind === 'comment').length

    const rendered = await renderDigest({
      pollTitle: poll.title,
      pollUrl: `${this.env.APP_URL}/p/${pollId}`,
      newVoters,
      newComments,
      locale: poll.owner.locale ?? 'en',
    })
    let ok = false
    try {
      ok = await this.mailer(this.env, { to: poll.owner.email, ...rendered })
    } catch (err) {
      console.error('[PollRoom] digest mail threw', err)
    }

    if (!ok) {
      const currentRetries = (await this.ctx.storage.get<number>(RETRY_COUNT_KEY)) ?? 0
      const nextRetries = currentRetries + 1
      if (nextRetries < MAX_RETRIES) {
        await this.ctx.storage.put(RETRY_COUNT_KEY, nextRetries)
        await this.ctx.storage.put(DIGEST_AT_KEY, now + RETRY_DELAY_MS)
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
  }

  async #rearm(): Promise<void> {
    const digestAt = await this.ctx.storage.get<number>(DIGEST_AT_KEY)
    const deadlineAt = await this.ctx.storage.get<number>(DEADLINE_AT_KEY)
    const candidates = [digestAt, deadlineAt].filter((v): v is number => v !== undefined)

    if (candidates.length > 0) {
      await this.ctx.storage.setAlarm(Math.min(...candidates))
    } else {
      await this.ctx.storage.deleteAlarm()
    }
  }
}
