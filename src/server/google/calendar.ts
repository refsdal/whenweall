import { and, eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { account } from '#/server/db/schema'
import { getAuth } from '#/server/auth/auth'
import type { Interval } from '#/lib/availability'

export class GoogleApiError extends Error {
  constructor(
    public status: number,
    message?: string,
  ) {
    super(message ?? `Google Calendar API error (${status})`)
    this.name = 'GoogleApiError'
  }
}

const CALENDAR_API = 'https://www.googleapis.com/calendar/v3'

export type CreateEventInput = {
  summary: string
  description?: string | null
  start: string
  end: string
  attendeeEmail: string
  timezone: string
}

/**
 * Thin fetch wrapper around the Google Calendar v3 REST API — the only place in the codebase
 * that talks to Google. `fetchImpl` is injectable so tests can mock the network (msw) instead of
 * this module.
 */
export function createCalendarClient(fetchImpl: typeof fetch = fetch) {
  async function getFreeBusy(
    accessToken: string,
    { timeMin, timeMax }: { timeMin: string; timeMax: string },
  ): Promise<Interval[]> {
    const res = await fetchImpl(`${CALENDAR_API}/freeBusy`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${accessToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ timeMin, timeMax, items: [{ id: 'primary' }] }),
    })
    if (!res.ok) throw new GoogleApiError(res.status)

    const data = (await res.json()) as {
      calendars?: { primary?: { busy?: { start: string; end: string }[] } }
    }
    return (data.calendars?.primary?.busy ?? []).map((b) => ({ start: b.start, end: b.end }))
  }

  async function createEvent(
    accessToken: string,
    input: CreateEventInput,
  ): Promise<{ eventId: string }> {
    const res = await fetchImpl(`${CALENDAR_API}/calendars/primary/events?sendUpdates=all`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${accessToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        summary: input.summary,
        description: input.description ?? undefined,
        start: { dateTime: input.start, timeZone: input.timezone },
        end: { dateTime: input.end, timeZone: input.timezone },
        attendees: [{ email: input.attendeeEmail }],
      }),
    })
    if (!res.ok) throw new GoogleApiError(res.status)

    const data = (await res.json()) as { id: string }
    return { eventId: data.id }
  }

  async function deleteEvent(accessToken: string, eventId: string): Promise<void> {
    const res = await fetchImpl(
      `${CALENDAR_API}/calendars/primary/events/${encodeURIComponent(eventId)}`,
      { method: 'DELETE', headers: { Authorization: `Bearer ${accessToken}` } },
    )
    // The event is already gone either way — Google returns 410 on a re-delete and some clients
    // see 404 — so both count as success rather than a sync failure to report to the organiser.
    if (!res.ok && res.status !== 404 && res.status !== 410) throw new GoogleApiError(res.status)
  }

  return { getFreeBusy, createEvent, deleteEvent }
}

/**
 * Resolves a currently-valid Google access token for `userId`'s linked Google account, or `null`
 * when there is no linked account or the token can't be obtained/refreshed — every failure here
 * degrades to "not connected" rather than throwing, since Google sync is always optional.
 *
 * Better-Auth 1.7's `POST /get-access-token` endpoint (`auth.api.getAccessToken`) takes a
 * Better-Auth *account row id* (`{ accountId, userId }`), not a `providerId` — there is no
 * "get the token for this user's google account" shortcut, so the account row is looked up via
 * Drizzle first to get that id, then handed to `getAccessToken`, which does the actual
 * refresh-if-needed and returns the (possibly refreshed) access token.
 */
export async function getGoogleAccessToken(userId: string): Promise<string | null> {
  const db = getDb()
  const row = await db.query.account.findFirst({
    where: and(eq(account.userId, userId), eq(account.providerId, 'google')),
  })
  if (!row) return null

  try {
    const tokens = await getAuth().api.getAccessToken({
      body: { accountId: row.id, userId },
    })
    return tokens?.accessToken ?? null
  } catch {
    return null
  }
}
