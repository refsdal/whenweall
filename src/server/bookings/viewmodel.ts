import type { BookingPageStatus, BookingStatus, CancelledBy } from '#/server/db/schema'
import type { Availability, DateOverrides } from './schemas'

export type PageSummary = {
  id: string
  slug: string
  title: string
  status: BookingPageStatus
  /** Count of confirmed bookings starting at or after "now" at the time of the query. */
  upcomingCount: number
  createdAt: string
  updatedAt: string
}

/** Full page detail as seen by its owner. */
export type PageView = {
  id: string
  slug: string
  title: string
  description: string | null
  location: string | null
  timezone: string
  slotDurationMin: number
  bufferBeforeMin: number
  bufferAfterMin: number
  minNoticeMin: number
  maxDaysAhead: number
  availability: Availability
  dateOverrides: DateOverrides | null
  googleSync: boolean
  reminders: boolean
  status: BookingPageStatus
  createdAt: string
  updatedAt: string
}

/** What a visitor sees at `/book/<handle>/<slug>` — no owner id, no email. */
export type PublicPageView = {
  id: string
  slug: string
  title: string
  description: string | null
  location: string | null
  timezone: string
  slotDurationMin: number
  bufferBeforeMin: number
  bufferAfterMin: number
  minNoticeMin: number
  maxDaysAhead: number
  availability: Availability
  dateOverrides: DateOverrides | null
  status: BookingPageStatus
  owner: { name: string }
}

export type BookingView = {
  id: string
  pageId: string
  startAt: string
  endAt: string
  visitorName: string
  visitorEmail: string
  visitorNote: string | null
  visitorTimezone: string
  visitorLocale: string | null
  status: BookingStatus
  cancelledBy: CancelledBy | null
  createdAt: string
}

/**
 * `getBookingForManage`'s return: the booking plus enough of its page to render a manage page.
 * Deliberately omits the page's `ownerId` — a token-authenticated visitor gets this same shape as
 * an authenticated owner, and nothing in this codebase reads the owner's internal id off it.
 */
export type BookingForManage = BookingView & {
  page: {
    id: string
    /** The organiser's public handle, or null while they haven't picked one — without it there
     * is no `/book/<handle>/<slug>` to reschedule against or to book again from. */
    handle: string | null
    slug: string
    title: string
    location: string | null
    timezone: string
    slotDurationMin: number
    owner: { name: string }
  }
}
