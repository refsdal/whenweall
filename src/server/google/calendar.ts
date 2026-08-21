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

const REQUIRED_CALENDAR_SCOPES = [
  'https://www.googleapis.com/auth/calendar.readonly',
  'https://www.googleapis.com/auth/calendar.events',
] as const

/**
 * Whether a Better-Auth `account.scope` string (space-separated, as Google reports it) grants
 * every scope Samla needs to read/write the linked calendar. Returns `null` — not `false` — when
 * `scope` itself wasn't recorded (older rows, or a provider that doesn't report it back), so
 * callers can tell "known to lack a scope" apart from "unknown, fall back to a live probe".
 */
function hasRequiredCalendarScopes(scope: string | null | undefined): boolean | null {
  if (!scope) return null
  const granted = new Set(scope.split(/\s+/).filter(Boolean))
  return REQUIRED_CALENDAR_SCOPES.every((s) => granted.has(s))
}

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
  // Known (not just unknown) to be missing a required calendar scope — don't bother refreshing a
  // token that can't do what the caller needs it for.
  if (hasRequiredCalendarScopes(row.scope) === false) return null

  try {
    const tokens = await getAuth().api.getAccessToken({
      body: { accountId: row.id, userId },
    })
    return tokens?.accessToken ?? null
  } catch {
    return null
  }
}

/**
 * Whether `userId` currently has a *usable* Google Calendar connection — verifying the granted
 * capability, not just that some token/row exists. `account.scope` is the source of truth when
 * Better-Auth recorded it: both calendar scopes present is `connected: true`, either missing is
 * `false`, with no network call needed either way. Only when `scope` itself is unavailable (older
 * rows, or a provider that never reported it back) does this fall back to a live freebusy probe —
 * treating a 401/403 (auth/permission failure) as not connected, and any other outcome (including
 * a transient error) as connected, since the token itself was obtained successfully.
 */
export async function getGoogleCalendarStatus(
  userId: string,
  fetchImpl: typeof fetch = fetch,
): Promise<{ connected: boolean }> {
  const db = getDb()
  const row = await db.query.account.findFirst({
    where: and(eq(account.userId, userId), eq(account.providerId, 'google')),
  })
  if (!row) return { connected: false }

  const scopesKnown = hasRequiredCalendarScopes(row.scope)
  if (scopesKnown !== null) return { connected: scopesKnown }

  const token = await getGoogleAccessToken(userId)
  if (!token) return { connected: false }

  try {
    const now = new Date()
    await createCalendarClient(fetchImpl).getFreeBusy(token, {
      timeMin: now.toISOString(),
      timeMax: new Date(now.getTime() + 60_000).toISOString(),
    })
    return { connected: true }
  } catch (err) {
    if (err instanceof GoogleApiError && (err.status === 401 || err.status === 403)) {
      return { connected: false }
    }
    return { connected: true }
  }
}
