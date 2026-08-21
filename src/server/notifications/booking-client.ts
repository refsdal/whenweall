import { env } from 'cloudflare:workers'
import type { BookingRoom } from '#/do/BookingRoom'
import type { BookingRoomEvent } from '#/do/booking-protocol'

/**
 * Thin client for talking to a booking page's `BookingRoom` durable object from server functions.
 * `notifyPageChanged` is best-effort: a stalled/evicted DO must never fail the request that
 * triggered the notification. `bookViaRoom`/`cancelViaRoom`/`rescheduleViaRoom` are NOT
 * best-effort — business errors (`SLOT_UNAVAILABLE`, `PAGE_PAUSED`, `BOOKING_PAST`, ...) thrown
 * inside the DO must reach the caller so the server function can map them to the right response.
 */
export function bookingRoom(pageId: string): DurableObjectStub<BookingRoom> {
  return env.BOOKING_ROOM.getByName(pageId)
}

export async function notifyPageChanged(pageId: string): Promise<void> {
  try {
    await bookingRoom(pageId).broadcast({ type: 'page.changed' } satisfies BookingRoomEvent)
  } catch (err) {
    console.error('[booking-client] notifyPageChanged failed', err)
  }
}

export function bookViaRoom(
  pageId: string,
  input: Parameters<BookingRoom['book']>[1],
  busy: Parameters<BookingRoom['book']>[2],
): Promise<Awaited<ReturnType<BookingRoom['book']>>> {
  return bookingRoom(pageId).book(pageId, input, busy)
}

export function cancelViaRoom(
  pageId: string,
  bookingId: string,
  by: Parameters<BookingRoom['cancel']>[2],
): Promise<Awaited<ReturnType<BookingRoom['cancel']>>> {
  return bookingRoom(pageId).cancel(pageId, bookingId, by)
}

export function rescheduleViaRoom(
  pageId: string,
  bookingId: string,
  startAt: string,
  busy: Parameters<BookingRoom['reschedule']>[3],
): Promise<Awaited<ReturnType<BookingRoom['reschedule']>>> {
  return bookingRoom(pageId).reschedule(pageId, bookingId, startAt, busy)
}
