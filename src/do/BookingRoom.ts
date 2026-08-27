import { DurableObject } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { createDb } from '#/server/db/client'
import { bookingPages, bookings, type CancelledBy } from '#/server/db/schema'
import { reportMailOutcome, sendMail } from '#/server/mailer/mailer'
import { createCalendarClient } from '#/server/google/calendar'
import {
  cancelBooking,
  createBooking,
  rescheduleBooking,
  type CreateBookingInput,
} from '#/server/bookings/bookings'
import type { Interval } from '#/lib/availability'
import type { BookingRoomEvent } from './booking-protocol'

const REMINDER_PREFIX = 'reminder:'
const REMINDER_LEAD_MS = 24 * 60 * 60_000

/**
 * One BookingRoom durable object per booking page (keyed by pageId). Serialises
 * book/cancel/reschedule so two visitors racing for the same slot can't both win (same pattern as
 * `PollRoom`'s claim mutex), broadcasts `page.changed` to any open public/manage page, and owns
 * the reminder alarm (one email pair 24h before each confirmed booking, opt-out per page).
 */
export class BookingRoom extends DurableObject<Env> {
  /**
   * Overridable seam for tests. `vi.mock` cannot reach code running inside a Durable Object's own
   * module graph in @cloudflare/vitest-plugin 1.0 — the DO's `alarm()`/RPC methods are loaded via
   * a separate `importModule()` path that does not share the test file's mock registry. Tests
   * instead override this instance field directly via `runInDurableObject` before invoking the
   * alarm. Production code never touches this — it always calls the real `sendMail`.
   */
  mailer: typeof sendMail = sendMail

  /**
   * Same DI-seam pattern as `mailer`, kept for parity and so a test can stub Google calls without
   * touching the network. Google Calendar reads/writes for a booking currently happen in the
   * `bookSlot`/`cancelBooking`/`rescheduleBooking` server functions (which already have the
   * caller's freebusy data and an access token in hand) rather than inside the DO itself — this
   * field is not invoked by any RPC method below, but exists here so that if a future change moves
   * that work into the room (e.g. to include it in the serialised section), it has a seam ready.
   */
  calendar: ReturnType<typeof createCalendarClient> = createCalendarClient()

  /**
   * Tail of an in-memory promise chain used to serialise `book`/`cancel`/`reschedule` calls. A
   * DO's "input gate" only guarantees ordering around `ctx.storage` operations — these RPCs
   * instead write to D1 (an external service, awaited like any `fetch`), so two calls arriving
   * back-to-back could otherwise interleave their availability check and write, and both see the
   * slot as free. Chaining every call onto this tail — synchronously, before either call's first
   * `await` — forces them to run one at a time in arrival order, which is what makes the
   * double-booking check atomic.
   */
  #queue: Promise<unknown> = Promise.resolve()

  #serialize<T>(fn: () => Promise<T>): Promise<T> {
    const run = this.#queue.then(fn, fn)
    // Keep the chain alive regardless of whether `fn` resolved or rejected — only the caller of
    // this particular call should see its outcome; the next queued call must still proceed.
    this.#queue = run.then(
      () => undefined,
      () => undefined,
    )
    return run
  }

  async fetch(request: Request): Promise<Response> {
    if (request.headers.get('Upgrade') !== 'websocket') {
      return new Response('Expected Upgrade: websocket', { status: 426 })
    }

    const { 0: client, 1: server } = new WebSocketPair()
    this.ctx.acceptWebSocket(server)

    return new Response(null, { status: 101, webSocket: client })
  }

  broadcast(event: BookingRoomEvent): number {
    return this.#send(event)
  }

  book(
    pageId: string,
    input: CreateBookingInput,
    busy: Interval[],
  ): Promise<{ bookingId: string; manageToken: string }> {
    return this.#serialize(async () => {
      const db = createDb(this.env.DB)
      const result = await createBooking(db, pageId, input, busy)
      this.#send({ type: 'page.changed' })

      const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
      if (page?.reminders) {
        await this.scheduleReminder(result.bookingId, input.startAt)
      }

      return result
    })
  }

  cancel(_pageId: string, bookingId: string, by: CancelledBy): Promise<{ changed: boolean }> {
    return this.#serialize(async () => {
      const db = createDb(this.env.DB)
      const result = await cancelBooking(db, bookingId, by)

      if (result.changed) {
        this.#send({ type: 'page.changed' })
        await this.cancelReminder(bookingId)
      }

      return result
    })
  }

  reschedule(
    pageId: string,
    bookingId: string,
    startAt: string,
    busy: Interval[],
  ): Promise<{ changed: true; previousStartAt: string }> {
    return this.#serialize(async () => {
      const db = createDb(this.env.DB)
      const result = await rescheduleBooking(db, bookingId, startAt, busy)
      this.#send({ type: 'page.changed' })

      const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
      if (page?.reminders) {
        await this.scheduleReminder(bookingId, startAt)
      } else {
        await this.cancelReminder(bookingId)
      }

      return result
    })
  }

  async scheduleReminder(bookingId: string, startAt: string): Promise<void> {
    await this.ctx.storage.put(`${REMINDER_PREFIX}${bookingId}`, new Date(startAt).getTime())
    await this.#rearmReminders()
  }

  async cancelReminder(bookingId: string): Promise<void> {
    await this.ctx.storage.delete(`${REMINDER_PREFIX}${bookingId}`)
    await this.#rearmReminders()
  }

  async alarm(): Promise<void> {
    try {
      const due = await this.#dueReminders()
      for (const [key, bookingId] of due) {
        try {
          await this.#sendReminderIfDue(bookingId)
        } catch (err) {
          console.error('[BookingRoom] reminder send failed', err)
        } finally {
          await this.ctx.storage.delete(key)
        }
      }
    } finally {
      await this.#rearmReminders()
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
  }

  webSocketError(ws: WebSocket, _error: unknown): void {
    try {
      ws.close()
    } catch {
      // Already closed/closing — ignore.
    }
  }

  #send(event: BookingRoomEvent): number {
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

  async #sendReminderIfDue(bookingId: string): Promise<void> {
    const db = createDb(this.env.DB)
    const booking = await db.query.bookings.findFirst({ where: eq(bookings.id, bookingId) })
    if (!booking || booking.status !== 'confirmed') return

    const page = await db.query.bookingPages.findFirst({
      where: eq(bookingPages.id, booking.pageId),
    })
    // A soft-deleted page (deletePage) is treated the same as "reminders off" — nothing left to
    // remind anyone about once the page itself is gone.
    if (!page || !page.reminders || page.deletedAt) return

    // Lazy for the same reason as PollRoom's digest templates: `bookings/emails` pulls React and
    // every booking email component in, and the reminder path is this room's only caller.
    const { sendBookingEmails } = await import('#/server/bookings/emails')
    reportMailOutcome(
      'booking.reminder',
      await sendBookingEmails(this.env, 'reminder', bookingId, { db, mailer: this.mailer }),
    )
  }

  /** `reminder:<bookingId>` keys, filtered to those whose 24h-before trigger time has passed. */
  async #dueReminders(): Promise<[string, string][]> {
    const now = Date.now()
    const all = await this.ctx.storage.list<number>({ prefix: REMINDER_PREFIX })
    const due: [string, string][] = []
    for (const [key, startAtMs] of all) {
      if (startAtMs - REMINDER_LEAD_MS <= now) {
        due.push([key, key.slice(REMINDER_PREFIX.length)])
      }
    }
    return due
  }

  async #rearmReminders(): Promise<void> {
    const all = await this.ctx.storage.list<number>({ prefix: REMINDER_PREFIX })
    let earliest: number | undefined
    for (const [, startAtMs] of all) {
      const triggerAt = startAtMs - REMINDER_LEAD_MS
      if (earliest === undefined || triggerAt < earliest) earliest = triggerAt
    }

    if (earliest !== undefined) {
      await this.ctx.storage.setAlarm(earliest)
    } else {
      await this.ctx.storage.deleteAlarm()
    }
  }
}
