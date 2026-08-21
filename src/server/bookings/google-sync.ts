import type { Db } from '#/server/db/client'
import type { BookingPage } from '#/server/db/schema'
import type { sendMail } from '#/server/mailer/mailer'
import { createCalendarClient, getGoogleAccessToken } from '#/server/google/calendar'
import * as bookingService from './bookings'
import { sendGoogleSyncFailedNotice, type BookingEmailEnv } from './emails'

/**
 * Best-effort Google Calendar sync for a booking (create on book/reschedule, delete on
 * cancel/reschedule) — kept in its own module, apart from `bookings.functions.ts`, so it can be
 * imported and exercised directly from tests (msw for the Google call, an injectable `mailer` for
 * the failure notice) without those exports dragging `cloudflare:workers` into the client bundle.
 * `bookings.functions.ts` is a `createServerFn` boundary file: the client build keeps whatever it
 * exports reachable (for the RPC stubs), so anything exported there that closes over the module's
 * own `env` (imported from `cloudflare:workers`) breaks `vite build` for the client target — this
 * module instead takes `env` as an explicit parameter and is only ever imported from inside
 * `.handler()` bodies, so it tree-shakes out of the client bundle the same way the Google calendar
 * client already did before this file existed.
 */

/**
 * Best-effort Google Calendar event create for a freshly booked slot; stores the event id on the
 * booking so cancel/reschedule can clean it up later. Never throws — a sync failure must not
 * fail a booking that already succeeded (spec decision 3: "Failures degrade gracefully"). On
 * failure, sends the organiser a one-off best-effort notice (never a retry of the sync itself).
 */
export async function syncGoogleEventCreate(
  env: BookingEmailEnv,
  db: Db,
  page: BookingPage,
  bookingId: string,
  input: { startAt: string; endAt: string; attendeeEmail: string },
  fetchImpl: typeof fetch = fetch,
  mailer?: typeof sendMail,
): Promise<void> {
  if (!page.googleSync) return
  try {
    const token = await getGoogleAccessToken(page.ownerId)
    if (!token) return
    const { eventId } = await createCalendarClient(fetchImpl).createEvent(token, {
      summary: page.title,
      description: page.description,
      start: input.startAt,
      end: input.endAt,
      attendeeEmail: input.attendeeEmail,
      timezone: page.timezone,
    })
    await bookingService.setGoogleEventId(db, bookingId, eventId)
  } catch (err) {
    console.error('[google-sync] Google event create failed', err)
    await sendGoogleSyncFailedNotice(env, bookingId, { db, mailer })
  }
}

/**
 * Best-effort Google Calendar event delete for a cancelled/rescheduled booking. Always attempts
 * the delete when a `googleEventId` is on record — even if the page's `googleSync` toggle has
 * since been turned off, an event created while sync was on doesn't clean itself up just because
 * the setting changed (spec ruling, finding 7). Clears `googleEventId` to `null` once the delete
 * either succeeds or the event was already gone (`deleteEvent` itself treats 404/410 as success).
 * On failure, sends the organiser a one-off best-effort notice and leaves `googleEventId` as-is
 * (nothing was actually cleaned up, so the id is still meaningful for a future retry/inspection).
 *
 * Returns `true` when the event was deleted or already gone (including "no usable Google token" —
 * there's nothing to clean up), `false` on failure — `syncGoogleEventsForReschedule` uses this to
 * decide whether it's safe to create the replacement event.
 */
export async function syncGoogleEventDelete(
  env: BookingEmailEnv,
  db: Db,
  page: BookingPage,
  bookingId: string,
  googleEventId: string,
  fetchImpl: typeof fetch = fetch,
  mailer?: typeof sendMail,
): Promise<boolean> {
  try {
    const token = await getGoogleAccessToken(page.ownerId)
    if (!token) return true
    await createCalendarClient(fetchImpl).deleteEvent(token, googleEventId)
    await bookingService.setGoogleEventId(db, bookingId, null)
    return true
  } catch (err) {
    console.error('[google-sync] Google event delete failed', err)
    await sendGoogleSyncFailedNotice(env, bookingId, { db, mailer })
    return false
  }
}

/**
 * Google Calendar delete-then-recreate for a reschedule, sequenced so a booking never ends up
 * pointing at a *new* Google event while the *old* one is still sitting on the organiser's
 * calendar under a `googleEventId` samla no longer knows about. Deletes `previousGoogleEventId`
 * (if any) via `syncGoogleEventDelete` and only creates the new event when that delete actually
 * succeeded — a failed delete leaves the booking's `googleEventId` pointed at the (still real,
 * still on the calendar) old event rather than overwriting it with an orphaned one, and relies on
 * `syncGoogleEventDelete`'s own best-effort organiser notice to surface the problem (no second
 * notice fires here for a create that was deliberately skipped).
 */
export async function syncGoogleEventsForReschedule(
  env: BookingEmailEnv,
  db: Db,
  page: BookingPage,
  bookingId: string,
  previousGoogleEventId: string | null,
  input: { startAt: string; endAt: string; attendeeEmail: string },
  fetchImpl: typeof fetch = fetch,
  mailer?: typeof sendMail,
): Promise<void> {
  const deleteOk = previousGoogleEventId
    ? await syncGoogleEventDelete(
        env,
        db,
        page,
        bookingId,
        previousGoogleEventId,
        fetchImpl,
        mailer,
      )
    : true

  if (deleteOk && page.googleSync) {
    await syncGoogleEventCreate(env, db, page, bookingId, input, fetchImpl, mailer)
  }
}
