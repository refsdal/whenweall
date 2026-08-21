import * as z from 'zod'

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

/**
 * Shape of a booking page id as minted by `newId()` (`nanoid(16)`, the library's default
 * URL-safe alphabet): 16 characters of `A-Za-z0-9_-`. Same role as polls' `pollIdSchema` — a
 * cheap, pre-database sanity check on a path param before it reaches a Durable Object lookup
 * (see `src/routes/api/bookings/$pageId/ws.ts`) — but sized to this id's actual format rather
 * than reusing `pollIdSchema`'s 12-char alnum-only pattern, which real page ids don't match.
 */
export const pageIdSchema = z.string().regex(/^[A-Za-z0-9_-]{16}$/)

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

export type TimeRange = z.infer<typeof timeRangeSchema>

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

export type Availability = z.infer<typeof availabilitySchema>

const DATE_KEY_RE = /^\d{4}-\d{2}-\d{2}$/

export const dateOverridesSchema = z
  .record(z.string(), dayRangesSchema)
  .refine((obj) => Object.keys(obj).every((k) => DATE_KEY_RE.test(k)), {
    message: "Override keys must be 'YYYY-MM-DD'",
  })
  .refine((obj) => Object.keys(obj).length <= LIMITS.overrideDays, {
    message: `At most ${LIMITS.overrideDays} date overrides`,
  })

export type DateOverrides = z.infer<typeof dateOverridesSchema>

function isValidTimeZone(tz: string): boolean {
  try {
    const check = new Intl.DateTimeFormat(undefined, { timeZone: tz })
    return Boolean(check)
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

export const updateBookingPageSchema = createBookingPageSchema.partial().extend({
  pageId: z.string(),
  status: z.enum(['active', 'paused']).optional(),
})

export type UpdateBookingPageInput = z.infer<typeof updateBookingPageSchema>

// No `timezone` field: availability is computed against the page's own configured timezone
// (`page.timezone`, via `pageRulesFrom`/`generateSlots`), and slots are grouped into the
// visitor's local days entirely client-side — the visitor's zone never needs to reach the server.
export const publicAvailabilityQuerySchema = z
  .object({
    handle: handleSchema,
    slug: slugSchema,
    from: z.iso.date(),
    to: z.iso.date(),
  })
  .refine(
    (data) => {
      const fromMs = new Date(`${data.from}T00:00:00Z`).getTime()
      const toMs = new Date(`${data.to}T00:00:00Z`).getTime()
      const days = (toMs - fromMs) / 86_400_000
      return days >= 0 && days <= LIMITS.publicWindowDays
    },
    { message: `Window must be between 0 and ${LIMITS.publicWindowDays} days`, path: ['to'] },
  )

export type PublicAvailabilityQueryInput = z.infer<typeof publicAvailabilityQuerySchema>

export const bookSlotSchema = z.object({
  pageId: z.string(),
  startAt: z.iso.datetime(),
  name: z.string().trim().min(1).max(LIMITS.name),
  email: z.email().max(254),
  note: z.string().trim().max(LIMITS.note).optional(),
  timezone: timezoneSchema,
  turnstileToken: z.string().optional(),
})

export type BookSlotInput = z.infer<typeof bookSlotSchema>

export const manageBookingSchema = z.object({
  bookingId: z.string(),
  token: z.string().optional(), // owner path has no token
})

export type ManageBookingInput = z.infer<typeof manageBookingSchema>

export const rescheduleSchema = manageBookingSchema.extend({
  startAt: z.iso.datetime(),
})

export type RescheduleInput = z.infer<typeof rescheduleSchema>
