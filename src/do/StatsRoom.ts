import { DurableObject } from 'cloudflare:workers'
import { count, isNull } from 'drizzle-orm'
import type { Answer } from '#/lib/scoring'
import { createDb } from '#/server/db/client'
import { polls, votes } from '#/server/db/schema'
import { EMPTY_STATS, type StatsEvent, type UsageStats } from './stats-protocol'

/** How often the in-memory delta is written to storage. Bounds storage writes at ~6/minute
 * regardless of traffic, at the cost of losing up to this much on an eviction — an acceptable
 * trade for a marketing counter, and one that must not be copied anywhere that matters. */
export const FLUSH_INTERVAL_MS = 10_000

/** A burst of votes should tick smoothly, not flood every connected browser with frames. */
export const BROADCAST_THROTTLE_MS = 2_000

const COUNTERS_KEY = 'counters'
const SEEDED_KEY = 'seeded'

const ANSWER_FIELD: Record<Answer, keyof UsageStats> = {
  yes: 'responsesYes',
  ifneedbe: 'responsesIfNeedBe',
  no: 'responsesNo',
}

/**
 * One global durable object (addressed by the constant name `global`) holding anonymous usage
 * totals for the landing page.
 *
 * Increments arrive as fire-and-forget RPC and accumulate in `#delta`; an alarm flushes them to
 * storage. Reads return the persisted counters plus whatever is currently un-flushed, so a caller
 * never sees a number go backwards just because a flush has not happened yet.
 */
export class StatsRoom extends DurableObject<Env> {
  /** Un-flushed increments. Lives only in memory between alarms. */
  #delta: UsageStats = { ...EMPTY_STATS }

  /** Wall-clock of the last broadcast, for throttling. */
  #lastBroadcastAt = 0

  async fetch(request: Request): Promise<Response> {
    if (request.headers.get('Upgrade') !== 'websocket') {
      return new Response('Expected Upgrade: websocket', { status: 426 })
    }

    const { 0: client, 1: server } = new WebSocketPair()
    this.ctx.acceptWebSocket(server)

    // Send the current numbers immediately so a socket that connects between broadcasts is not
    // stuck showing whatever the page was server-rendered with.
    try {
      server.send(JSON.stringify({ type: 'stats', stats: await this.read() } satisfies StatsEvent))
    } catch {
      // Socket already gone before we could greet it; nothing to do.
    }

    return new Response(null, { status: 101, webSocket: client })
  }

  /** Current totals: persisted counters plus the un-flushed delta. Seeds on first call. */
  async read(): Promise<UsageStats> {
    const stored = await this.#seededCounters()
    return {
      pollsCreated: stored.pollsCreated + this.#delta.pollsCreated,
      responsesYes: stored.responsesYes + this.#delta.responsesYes,
      responsesIfNeedBe: stored.responsesIfNeedBe + this.#delta.responsesIfNeedBe,
      responsesNo: stored.responsesNo + this.#delta.responsesNo,
    }
  }

  async recordPollCreated(): Promise<void> {
    this.#delta.pollsCreated += 1
    await this.#afterIncrement()
  }

  async recordResponses(answers: Answer[]): Promise<void> {
    if (answers.length === 0) return
    for (const answer of answers) {
      const field = ANSWER_FIELD[answer]
      if (field) this.#delta[field] += 1
    }
    await this.#afterIncrement()
  }

  async alarm(): Promise<void> {
    await this.#flush()
  }

  webSocketClose(ws: WebSocket, code: number, reason: string): void {
    try {
      ws.close(code, reason)
    } catch {
      // Already closed/closing — ignore.
    }
  }

  webSocketError(ws: WebSocket): void {
    try {
      ws.close()
    } catch {
      // Already closed/closing — ignore.
    }
  }

  async #afterIncrement(): Promise<void> {
    // Arm the flush alarm if one is not already pending. `getAlarm` is cheap and this keeps the
    // alarm at one-per-window rather than pushing it further out on every single increment.
    if ((await this.ctx.storage.getAlarm()) === null) {
      await this.ctx.storage.setAlarm(Date.now() + FLUSH_INTERVAL_MS)
    }
    await this.#broadcastThrottled()
  }

  async #flush(): Promise<void> {
    const delta = this.#delta
    const hasDelta = Object.values(delta).some((v) => v !== 0)
    if (!hasDelta) return

    const stored = await this.#seededCounters()
    // Reset before awaiting the write so increments arriving during it are not double-counted by
    // the next flush — they land in the fresh delta and are added on top of what we just stored.
    this.#delta = { ...EMPTY_STATS }

    await this.ctx.storage.put(COUNTERS_KEY, {
      pollsCreated: stored.pollsCreated + delta.pollsCreated,
      responsesYes: stored.responsesYes + delta.responsesYes,
      responsesIfNeedBe: stored.responsesIfNeedBe + delta.responsesIfNeedBe,
      responsesNo: stored.responsesNo + delta.responsesNo,
    } satisfies UsageStats)
  }

  /**
   * The persisted counters, seeding from D1 on first access. The whole thing runs inside the DO's
   * input gate, so two concurrent first-requests cannot both seed.
   */
  async #seededCounters(): Promise<UsageStats> {
    const stored = await this.ctx.storage.get<UsageStats>(COUNTERS_KEY)
    if (stored) return stored

    const seeded = await this.ctx.storage.get<boolean>(SEEDED_KEY)
    if (seeded) return { ...EMPTY_STATS }

    const counters = await this.#seedFromD1()
    await this.ctx.storage.put(COUNTERS_KEY, counters)
    await this.ctx.storage.put(SEEDED_KEY, true)
    return counters
  }

  /**
   * Starting totals taken from the existing data. `votes` holds *current* state, so answers that
   * were later changed or withdrawn are not represented and deleted polls are excluded — the seed
   * is a floor rather than a true history. Understating is the safe direction, and everything
   * after the seed is counted exactly.
   */
  async #seedFromD1(): Promise<UsageStats> {
    const counters: UsageStats = { ...EMPTY_STATS }

    try {
      const db = createDb(this.env.DB)

      const [pollRows, voteRows] = await Promise.all([
        db.select({ value: count() }).from(polls).where(isNull(polls.deletedAt)),
        db.select({ answer: votes.answer, value: count() }).from(votes).groupBy(votes.answer),
      ])

      counters.pollsCreated = pollRows[0]?.value ?? 0
      for (const row of voteRows) {
        const field = ANSWER_FIELD[row.answer]
        if (field) counters[field] = row.value
      }
    } catch (err) {
      // A failed seed must not make the counters unreadable — start from zero and let live
      // increments accumulate. Logged so a persistently zero counter is diagnosable.
      console.error('[StatsRoom] seed from D1 failed', err)
    }

    return counters
  }

  async #broadcastThrottled(): Promise<void> {
    const sockets = this.ctx.getWebSockets()
    if (sockets.length === 0) return

    const now = Date.now()
    if (now - this.#lastBroadcastAt < BROADCAST_THROTTLE_MS) return
    this.#lastBroadcastAt = now

    const payload = JSON.stringify({
      type: 'stats',
      stats: await this.read(),
    } satisfies StatsEvent)

    for (const ws of sockets) {
      try {
        ws.send(payload)
      } catch {
        // Drop sockets that can't be sent to.
      }
    }
  }
}
