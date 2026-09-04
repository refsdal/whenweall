/**
 * Hand-mirrors of the Go backend's response shapes — internal/polls/viewmodel.go,
 * internal/bookings/viewmodel.go, internal/admin/{stats,users,audit}.go, internal/jobs, and
 * internal/polls/handlers.go's publicConfigResponse. These are the BINDING contract (per the
 * plan-8 brief): every field name/optionality below was read off the Go struct tags, not the old
 * TS server code the fields happen to resemble.
 */

// ---- polls ---------------------------------------------------------------------------------

export type PollType = 'datetime' | 'options' | 'signup'
export type PollStatus = 'open' | 'closed' | 'finalized'
export type OptionKind = 'date' | 'datetime' | 'text'
export type Answer = 'yes' | 'ifneedbe' | 'no'

export type PollOptionView = {
  id: string
  position: number
  kind: OptionKind
  startAt: string | null
  endAt: string | null
  label: string | null
  capacity: number | null
}

export type ParticipantView = {
  id: string
  name: string
  userId: string | null
  hasEmail: boolean
  /** optionId -> answer */
  votes: Record<string, Answer>
  createdAt: string
}

export type CommentView = {
  id: string
  authorName: string
  body: string
  createdAt: string
  userId: string | null
  participantId: string | null
}

export type PollSettingsView = {
  requireParticipantEmail: boolean
  allowComments: boolean
  allowIfNeedBe: boolean
  signupMaxClaims: number
}

/** A poll's own per-viewer notification state (nil on the Go side for a non-member/anonymous
 * viewer) — `channels`/`defaults` are the same per-event on/off grid `NotificationGrid` types. */
export type NotificationsView = {
  channels: Record<string, unknown> | null
  defaults: Record<string, unknown> | null
  following: boolean
}

export type PollOwnerView = { name: string }

export type OptionScore = { yes: number; ifneedbe: number; no: number; score: number }

export type ClaimView = { count: number; capacity: number | null; full: boolean }

export type PollView = {
  id: string
  type: PollType
  title: string
  description: string | null
  location: string | null
  timezone: string
  status: PollStatus
  deadlineAt: string | null
  finalizedOptionId: string | null
  createdAt: string
  settings: PollSettingsView
  notifications: NotificationsView | null
  owner: PollOwnerView
  isOwner: boolean
  options: PollOptionView[]
  participants: ParticipantView[]
  comments: CommentView[]
  scores: Record<string, OptionScore>
  bestOptionId: string | null
  claims: Record<string, ClaimView>
}

export type PollSummary = {
  id: string
  title: string
  type: PollType
  status: PollStatus
  deadlineAt: string | null
  participantCount: number
  claimCount: number
  createdAt: string
  updatedAt: string
}

// ---- bookings --------------------------------------------------------------------------------

export type BookingPageStatus = 'active' | 'paused'

/** One day's windows, `"HH:mm"` on a 15-minute grid. Mirrors bookings/schemas.go's TimeRange. */
export type TimeRange = { start: string; end: string }

/** Weekday key ('0'..'6', Sunday-first, matching `Date#getDay`) -> that day's windows. */
export type Availability = Record<string, TimeRange[]>

/** `'YYYY-MM-DD'` -> that date's windows, overriding the weekly default for just that day. */
export type DateOverrides = Record<string, TimeRange[]>

export type PageSummary = {
  id: string
  slug: string
  title: string
  status: BookingPageStatus
  upcomingCount: number
  createdAt: string
  updatedAt: string
}

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
  // A nil Go map serializes as JSON `null`, not `{}` — an unset overrides map comes back this way
  // (internal/bookings/viewmodel.go's `PageView.DateOverrides` has no `omitempty`/pointer to
  // prevent it).
  dateOverrides: DateOverrides | null
  googleSync: boolean
  reminders: boolean
  status: BookingPageStatus
  createdAt: string
  updatedAt: string
}

export type PublicPageOwnerView = { name: string }

export type PublicPageView = {
  id: string
  handle: string
  slug: string
  title: string
  description: string | null
  location: string | null
  timezone: string
  slotDurationMin: number
  maxDaysAhead: number
  status: BookingPageStatus
  owner: PublicPageOwnerView
}

export type BookingStatus = 'confirmed' | 'cancelled'

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
  cancelledBy: string | null
  createdAt: string
}

export type ManagedBookingPageView = {
  id: string
  handle: string | null
  slug: string
  title: string
  location: string | null
  timezone: string
  slotDurationMin: number
  owner: PublicPageOwnerView
}

/** getManagedBooking's response shape (internal/bookings/viewmodel.go's ManagedBookingView). */
export type BookingForManage = BookingView & { page: ManagedBookingPageView }

// ---- admin -------------------------------------------------------------------------------------

export type AdminCount = { total: number; last7: number; last30: number }

/** internal/admin/stats.go's DashboardStats — flattened (no growth/revenue nesting), and revenue
 * is gone entirely (billing was removed from this rewrite). */
export type AdminStats = {
  users: AdminCount
  orgs: AdminCount
  polls: AdminCount
  pollsFinalized: number
  signupSheets: AdminCount
  participants: AdminCount
  bookingPages: AdminCount
  bookings: AdminCount
  mailQueueDepth: number
  failedJobs: number
}

export type AdminUserRow = {
  id: string
  email: string
  name: string
  emailVerified: boolean
  staff: boolean
  locked: boolean
  createdAt: string
}

export type OrgMembership = { id: string; name: string; slug: string; roles: string[] }

export type UserCounts = { polls: number; bookingPages: number; bookings: number }

export type AdminUserDetail = AdminUserRow & {
  lockReason: string | null
  orgs: OrgMembership[]
  counts: UserCounts
}

export type AuditEntry = {
  id: string
  actorUserId: string | null
  actorEmail: string
  action: string
  targetType: string
  targetId: string | null
  reason: string | null
  metadata: unknown
  createdAt: string
}

/** internal/admin/handlers.go's FailedJobView — deliberately no `payload` field (it may hold
 * addresses/tokens). `payloadExpired` is true once the deadletter:sweep housekeeping job has
 * purged a dead mail job's payload; the backend answers `409 payload_expired` to a retry of it. */
export type FailedJobView = {
  id: string
  kind: string
  attempts: number
  lastError: string | null
  runAt: string
  payloadExpired: boolean
}

// ---- config -------------------------------------------------------------------------------------

/** GET /api/v1/config's response (internal/polls/handlers.go's publicConfigResponse). */
export type PublicConfig = {
  turnstileSiteKey?: string
  googleEnabled: boolean
  oidcEnabled: boolean
  oidcName?: string
}
