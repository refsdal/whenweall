import * as z from 'zod'
import { api, ApiError } from '#/api/client'
import { getLocale } from '#/lib/i18n'
import type {
  Availability,
  BookingForManage,
  BookingView,
  DateOverrides,
  PageSummary,
  PageView,
  PublicPageView,
} from '#/api/types'

/**
 * Per-module functions mirroring the old `pages.functions.ts`/`bookings.functions.ts` names 1:1,
 * against internal/bookings/handlers.go's REST surface.
 */

export const LIMITS = {
  handleMin: 3,
  handleMax: 30,
  slugMin: 3,
  slugMax: 30,
  title: 200,
  description: 2000,
  location: 500,
  note: 1000,
  name: 80,
  rangesPerDay: 20,
  overrideDays: 366,
  slotDurationMin: 15,
  slotDurationMax: 480,
  bufferMin: 0,
  bufferMax: 120,
  minNoticeMin: 0,
  minNoticeMax: 10080,
  minNoticeDefault: 120,
  maxDaysAheadMin: 1,
  maxDaysAheadMax: 365,
  maxDaysAheadDefault: 60,
  publicWindowDays: 62,
} as const

const HANDLE_SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$/

export const handleSchema = z
  .string()
  .min(LIMITS.handleMin)
  .max(LIMITS.handleMax)
  .regex(HANDLE_SLUG_RE, 'Must be lowercase letters, digits and hyphens')

export const slugSchema = z
  .string()
  .min(LIMITS.slugMin)
  .max(LIMITS.slugMax)
  .regex(HANDLE_SLUG_RE, 'Must be lowercase letters, digits and hyphens')

const HHMM_RE = /^([01]\d|2[0-3]):(00|15|30|45)$/

function toMinutes(hhmm: string): number {
  const [h, m] = hhmm.split(':').map(Number)
  return h * 60 + m
}

export const timeRangeSchema = z
  .object({
    start: z.string().regex(HHMM_RE, 'Must be HH:mm on a 15-minute grid'),
    end: z.string().regex(HHMM_RE, 'Must be HH:mm on a 15-minute grid'),
  })
  .refine((r) => toMinutes(r.start) < toMinutes(r.end), {
    message: 'start must be before end',
    path: ['end'],
  })

/** Ranges for a single day: at most LIMITS.rangesPerDay, sorted and non-overlapping. */
const dayRangesSchema = z
  .array(timeRangeSchema)
  .max(LIMITS.rangesPerDay)
  .superRefine((ranges, ctx) => {
    for (let i = 1; i < ranges.length; i++) {
      const prev = ranges[i - 1]
      const cur = ranges[i]
      if (toMinutes(cur.start) < toMinutes(prev.start)) {
        ctx.addIssue({ code: 'custom', message: 'Ranges must be sorted', path: [i] })
        continue
      }
      if (toMinutes(cur.start) < toMinutes(prev.end)) {
        ctx.addIssue({ code: 'custom', message: 'Ranges must not overlap', path: [i] })
      }
    }
  })

const WEEKDAY_KEY_RE = /^[0-6]$/
export const availabilitySchema = z
  .record(z.string(), dayRangesSchema)
  .refine((obj) => Object.keys(obj).every((k) => WEEKDAY_KEY_RE.test(k)), {
    message: "Weekday keys must be '0'..'6'",
  })

const DATE_KEY_RE = /^\d{4}-\d{2}-\d{2}$/
export const dateOverridesSchema = z
  .record(z.string(), dayRangesSchema)
  .refine((obj) => Object.keys(obj).every((k) => DATE_KEY_RE.test(k)), {
    message: "Override keys must be 'YYYY-MM-DD'",
  })
  .refine((obj) => Object.keys(obj).length <= LIMITS.overrideDays, {
    message: `At most ${LIMITS.overrideDays} date overrides`,
  })

function isValidTimeZone(tz: string): boolean {
  try {
    return Boolean(new Intl.DateTimeFormat(undefined, { timeZone: tz }))
  } catch {
    return false
  }
}

const timezoneSchema = z
  .string()
  .min(1)
  .max(64)
  .refine(isValidTimeZone, { message: 'Invalid IANA timezone' })

export const createBookingPageSchema = z.object({
  slug: slugSchema,
  title: z.string().trim().min(1).max(LIMITS.title),
  description: z.string().trim().max(LIMITS.description).optional(),
  location: z.string().trim().max(LIMITS.location).optional(),
  timezone: timezoneSchema,
  slotDurationMin: z.number().int().min(LIMITS.slotDurationMin).max(LIMITS.slotDurationMax),
  bufferBeforeMin: z.number().int().min(LIMITS.bufferMin).max(LIMITS.bufferMax),
  bufferAfterMin: z.number().int().min(LIMITS.bufferMin).max(LIMITS.bufferMax),
  minNoticeMin: z.number().int().min(LIMITS.minNoticeMin).max(LIMITS.minNoticeMax),
  maxDaysAhead: z.number().int().min(LIMITS.maxDaysAheadMin).max(LIMITS.maxDaysAheadMax),
  availability: availabilitySchema,
  dateOverrides: dateOverridesSchema.optional(),
  googleSync: z.boolean(),
  reminders: z.boolean(),
})

export type CreateBookingPageInput = z.infer<typeof createBookingPageSchema>

/** A full replacement, not a partial patch: `PATCH /booking-pages/{id}` (internal/bookings/
 * handlers.go handleUpdatePage) overwrites every field and rejects an omitted availability or
 * status with 422, so the client schema requires them too. `draftToUpdate` (editor-state.ts)
 * always sends the whole draft. */
export const updateBookingPageSchema = createBookingPageSchema.extend({
  pageId: z.string(),
  status: z.enum(['active', 'paused']),
})

export type UpdateBookingPageInput = z.infer<typeof updateBookingPageSchema>

// ---- booking pages (owner-facing) --------------------------------------------------------------

export function createBookingPage(input: CreateBookingPageInput): Promise<PageView> {
  return api<PageView>('POST', '/api/v1/booking-pages', input)
}

export function listMyBookingPages(): Promise<PageSummary[]> {
  return api<PageSummary[]>('GET', '/api/v1/booking-pages')
}

export function getBookingPage(pageId: string): Promise<PageView> {
  return api<PageView>('GET', `/api/v1/booking-pages/${pageId}`)
}

export async function updateBookingPage(input: UpdateBookingPageInput): Promise<PageView> {
  const { pageId, ...body } = input
  return api<PageView>('PATCH', `/api/v1/booking-pages/${pageId}`, body)
}

export async function deleteBookingPage(pageId: string): Promise<void> {
  await api('DELETE', `/api/v1/booking-pages/${pageId}`)
}

export function listPageBookings(
  pageId: string,
  range: { from: string; to: string },
): Promise<BookingView[]> {
  return api<BookingView[]>('GET', `/api/v1/booking-pages/${pageId}/bookings`, undefined, {
    query: range,
  })
}

export function getGoogleCalendarStatus(pageId: string): Promise<{ connected: boolean }> {
  return api<{ available: boolean }>('GET', `/api/v1/booking-pages/${pageId}/google-status`).then(
    (r) => ({ connected: r.available }),
  )
}

export async function setHandle(handle: string): Promise<void> {
  await api('POST', '/api/v1/org/handle', { handle })
}

export async function disconnectGoogleCalendar(): Promise<void> {
  await api('POST', '/api/v1/me/google/disconnect')
}

// ---- public booking flow -------------------------------------------------------------------

/** `null` for an unknown handle/slug — the old TS `getPublicPage` returned `null` and the route's
 * `notFoundComponent` (`routes/book/$handle/$slug.tsx`) depends on that; `api()` itself throws
 * `ApiError('not_found')` on the Go 404, so it's translated back here. Any other failure (rate
 * limit, 5xx, network) still throws, so the generic error card keeps its retry button for those. */
export async function getPublicPage(handle: string, slug: string): Promise<PublicPageView | null> {
  try {
    return await api<PublicPageView>('GET', `/api/v1/book/${handle}/${slug}`)
  } catch (err) {
    if (err instanceof ApiError && err.code === 'not_found') return null
    throw err
  }
}

/** Slot start times only — the Go endpoint's own response shape (`{"slots": [iso, ...]}`,
 * internal/bookings/handlers.go's handlePublicAvailability). `null` on `not_found`, same as
 * `getPublicPage`, so `getPublicAvailability` can resolve `null` without `Promise.all` rejecting
 * on the availability half of the pair. */
async function fetchPublicSlots(input: {
  handle: string
  slug: string
  from: string
  to: string
}): Promise<string[] | null> {
  try {
    const result = await api<{ slots: string[] }>(
      'GET',
      `/api/v1/book/${input.handle}/${input.slug}/availability`,
      undefined,
      { query: { from: input.from, to: input.to } },
    )
    return result.slots
  } catch (err) {
    if (err instanceof ApiError && err.code === 'not_found') return null
    throw err
  }
}

/** `{start, end}` pairs — `end` is computed here from `page.slotDurationMin`, the same width every
 * slot on a page has, since the Go endpoint only returns start times (unlike the old TS
 * `getPublicAvailability`, which already returned `Interval[]`). `null` when the page is unknown. */
export async function getPublicAvailability(input: {
  handle: string
  slug: string
  from: string
  to: string
}): Promise<{ page: PublicPageView; slots: { start: string; end: string }[] } | null> {
  const [page, starts] = await Promise.all([
    getPublicPage(input.handle, input.slug),
    fetchPublicSlots(input),
  ])
  if (!page || starts === null) return null
  const slots = starts.map((start) => ({
    start,
    end: new Date(new Date(start).getTime() + page.slotDurationMin * 60_000).toISOString(),
  }))
  return { page, slots }
}

/** handleBook's response shape (internal/bookings/handlers.go): `{booking, manageToken}`, per the
 * plan's own route table (docs/superpowers/plans/2026-09-01-go-rewrite-05-bookings.md) — NOT the
 * flat `{bookingId, manageToken}` the old TS route (`bookings.functions.ts`'s `book`) returned. */
export type BookResult = { booking: BookingView; manageToken: string }

/** The Go endpoint is mounted at `/api/v1/book/{org}/{page}/bookings` (org/page SLUGS, not an
 * internal page id) — `handle`/`slug` come straight off the `PublicPageView` the visitor is
 * already looking at. */
export function bookSlot(
  handle: string,
  slug: string,
  input: { startAt: string; name: string; email: string; note?: string; timezone: string },
  opts?: { captchaToken?: string },
): Promise<BookResult> {
  return api<BookResult>(
    'POST',
    `/api/v1/book/${handle}/${slug}/bookings`,
    {
      startAt: input.startAt,
      name: input.name,
      email: input.email,
      note: input.note,
      timezone: input.timezone,
      locale: getLocale(),
    },
    { captchaToken: opts?.captchaToken },
  )
}

/** A plain link, not a fetch call — the browser downloads this directly, mirroring polls'
 * `calendarIcsUrl`/`rosterCsvUrl` (polls.ts). Unlike those two (owner-session cookie auth,
 * same-origin), a booking's manage token IS the credential here: the visitor viewing this link
 * has no session at all, so `t` carries the same query-param token every other manage-token
 * endpoint in this file does. */
export function bookingCalendarIcsUrl(bookingId: string, token?: string): string {
  return token
    ? `/api/v1/bookings/${bookingId}/calendar.ics?t=${encodeURIComponent(token)}`
    : `/api/v1/bookings/${bookingId}/calendar.ics`
}

export function getManagedBooking(bookingId: string, token?: string): Promise<BookingForManage> {
  return api<BookingForManage>('GET', `/api/v1/bookings/${bookingId}/manage`, undefined, {
    query: token ? { t: token } : undefined,
  })
}

export async function cancelBooking(bookingId: string, token?: string): Promise<void> {
  await api('POST', `/api/v1/bookings/${bookingId}/cancel`, undefined, {
    query: token ? { t: token } : undefined,
  })
}

export type RescheduleResult = { booking: BookingView; previousStartAt: string }

export function rescheduleBooking(
  bookingId: string,
  startAt: string,
  token?: string,
): Promise<RescheduleResult> {
  return api<RescheduleResult>(
    'POST',
    `/api/v1/bookings/${bookingId}/reschedule`,
    { startAt },
    { query: token ? { t: token } : undefined },
  )
}

export type { Availability, DateOverrides }
