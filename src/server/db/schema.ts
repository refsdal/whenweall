import { relations, sql } from 'drizzle-orm'
import { index, integer, primaryKey, sqliteTable, text, uniqueIndex } from 'drizzle-orm/sqlite-core'
import type { NotificationGrid } from '#/lib/notifications'
import { organization, user } from './auth-schema'

export * from './auth-schema'

export const POLL_TYPES = ['datetime', 'options', 'signup'] as const
export const POLL_STATUSES = ['open', 'closed', 'finalized'] as const
export const OPTION_KINDS = ['date', 'datetime', 'text'] as const
export const ANSWERS = ['yes', 'ifneedbe', 'no'] as const
export type PollType = (typeof POLL_TYPES)[number]
export type PollStatus = (typeof POLL_STATUSES)[number]
export type OptionKind = (typeof OPTION_KINDS)[number]
export type Answer = (typeof ANSWERS)[number]

const bool = (name: string, def: boolean) =>
  integer(name, { mode: 'boolean' }).notNull().default(def)

export const polls = sqliteTable(
  'polls',
  {
    id: text('id').primaryKey(),
    organizationId: text('organization_id')
      .notNull()
      .references(() => organization.id, { onDelete: 'cascade' }),
    createdBy: text('created_by').references(() => user.id, { onDelete: 'set null' }),
    type: text('type', { enum: POLL_TYPES }).notNull(),
    title: text('title').notNull(),
    description: text('description'),
    location: text('location'),
    timezone: text('timezone').notNull(),
    status: text('status', { enum: POLL_STATUSES }).notNull().default('open'),
    deadlineAt: text('deadline_at'),
    finalizedOptionId: text('finalized_option_id'),
    requireParticipantEmail: bool('require_participant_email', false),
    allowComments: bool('allow_comments', true),
    allowIfNeedBe: bool('allow_if_need_be', true),
    signupMaxClaims: integer('signup_max_claims').notNull().default(1),
    createdAt: text('created_at').notNull(),
    updatedAt: text('updated_at').notNull(),
    deletedAt: text('deleted_at'),
  },
  (t) => [index('polls_org_created_idx').on(t.organizationId, t.createdAt)],
)

export const pollOptions = sqliteTable(
  'poll_options',
  {
    id: text('id').primaryKey(),
    pollId: text('poll_id')
      .notNull()
      .references(() => polls.id, { onDelete: 'cascade' }),
    position: integer('position').notNull(),
    kind: text('kind', { enum: OPTION_KINDS }).notNull(),
    startAt: text('start_at'),
    endAt: text('end_at'),
    label: text('label'),
    capacity: integer('capacity'),
  },
  (t) => [index('poll_options_poll_position_idx').on(t.pollId, t.position)],
)

export const participants = sqliteTable(
  'participants',
  {
    id: text('id').primaryKey(),
    pollId: text('poll_id')
      .notNull()
      .references(() => polls.id, { onDelete: 'cascade' }),
    name: text('name').notNull(),
    email: text('email'),
    userId: text('user_id').references(() => user.id, { onDelete: 'set null' }),
    editTokenHash: text('edit_token_hash'),
    locale: text('locale'),
    createdAt: text('created_at').notNull(),
    updatedAt: text('updated_at').notNull(),
  },
  (t) => [index('participants_poll_idx').on(t.pollId)],
)

export const votes = sqliteTable(
  'votes',
  {
    participantId: text('participant_id')
      .notNull()
      .references(() => participants.id, { onDelete: 'cascade' }),
    optionId: text('option_id')
      .notNull()
      .references(() => pollOptions.id, { onDelete: 'cascade' }),
    answer: text('answer', { enum: ANSWERS }).notNull(),
  },
  (t) => [primaryKey({ columns: [t.participantId, t.optionId] })],
)

export const comments = sqliteTable(
  'comments',
  {
    id: text('id').primaryKey(),
    pollId: text('poll_id')
      .notNull()
      .references(() => polls.id, { onDelete: 'cascade' }),
    authorName: text('author_name').notNull(),
    participantId: text('participant_id').references(() => participants.id, {
      onDelete: 'set null',
    }),
    userId: text('user_id').references(() => user.id, { onDelete: 'set null' }),
    body: text('body').notNull(),
    createdAt: text('created_at').notNull(),
    deletedAt: text('deleted_at'),
  },
  (t) => [index('comments_poll_created_idx').on(t.pollId, t.createdAt)],
)

export const pollsRelations = relations(polls, ({ one, many }) => ({
  organization: one(organization, {
    fields: [polls.organizationId],
    references: [organization.id],
  }),
  owner: one(user, { fields: [polls.createdBy], references: [user.id] }),
  options: many(pollOptions),
  participants: many(participants),
  comments: many(comments),
}))
export const pollOptionsRelations = relations(pollOptions, ({ one, many }) => ({
  poll: one(polls, { fields: [pollOptions.pollId], references: [polls.id] }),
  votes: many(votes),
}))
export const participantsRelations = relations(participants, ({ one, many }) => ({
  poll: one(polls, { fields: [participants.pollId], references: [polls.id] }),
  votes: many(votes),
}))
export const votesRelations = relations(votes, ({ one }) => ({
  participant: one(participants, { fields: [votes.participantId], references: [participants.id] }),
  option: one(pollOptions, { fields: [votes.optionId], references: [pollOptions.id] }),
}))
export const commentsRelations = relations(comments, ({ one }) => ({
  poll: one(polls, { fields: [comments.pollId], references: [polls.id] }),
}))

export type Poll = typeof polls.$inferSelect
export type NewPoll = typeof polls.$inferInsert
export type PollOption = typeof pollOptions.$inferSelect
export type Participant = typeof participants.$inferSelect
export type Vote = typeof votes.$inferSelect
export type Comment = typeof comments.$inferSelect

export const BOOKING_PAGE_STATUSES = ['active', 'paused'] as const
export const BOOKING_STATUSES = ['confirmed', 'cancelled'] as const
export const CANCELLED_BY = ['visitor', 'organiser'] as const
export type BookingPageStatus = (typeof BOOKING_PAGE_STATUSES)[number]
export type BookingStatus = (typeof BOOKING_STATUSES)[number]
export type CancelledBy = (typeof CANCELLED_BY)[number]

export const bookingPages = sqliteTable(
  'booking_pages',
  {
    id: text('id').primaryKey(),
    organizationId: text('organization_id')
      .notNull()
      .references(() => organization.id, { onDelete: 'cascade' }),
    createdBy: text('created_by').references(() => user.id, { onDelete: 'set null' }),
    /** Whose calendar/availability this page reads and writes (Google sync) — set to the
     * creator on create, but reassignable later (e.g. a page handed off to a teammate). */
    memberUserId: text('member_user_id').references(() => user.id, { onDelete: 'set null' }),
    slug: text('slug').notNull(),
    title: text('title').notNull(),
    description: text('description'),
    location: text('location'),
    timezone: text('timezone').notNull(),
    slotDurationMin: integer('slot_duration_min').notNull(),
    bufferBeforeMin: integer('buffer_before_min').notNull(),
    bufferAfterMin: integer('buffer_after_min').notNull(),
    minNoticeMin: integer('min_notice_min').notNull(),
    maxDaysAhead: integer('max_days_ahead').notNull(),
    availability: text('availability').notNull(),
    dateOverrides: text('date_overrides'),
    googleSync: bool('google_sync', false),
    reminders: bool('reminders', true),
    status: text('status', { enum: BOOKING_PAGE_STATUSES }).notNull().default('active'),
    createdAt: text('created_at').notNull(),
    updatedAt: text('updated_at').notNull(),
    deletedAt: text('deleted_at'),
  },
  (t) => [
    // Partial so a soft-deleted page's slug can be reused by a new (or re-created) page — only
    // live pages (deleted_at IS NULL) compete for a slug.
    uniqueIndex('booking_pages_org_slug_uidx')
      .on(t.organizationId, t.slug)
      .where(sql`${t.deletedAt} IS NULL`),
  ],
)

export const bookings = sqliteTable(
  'bookings',
  {
    id: text('id').primaryKey(),
    pageId: text('page_id')
      .notNull()
      .references(() => bookingPages.id, { onDelete: 'cascade' }),
    startAt: text('start_at').notNull(),
    endAt: text('end_at').notNull(),
    visitorName: text('visitor_name').notNull(),
    visitorEmail: text('visitor_email').notNull(),
    visitorNote: text('visitor_note'),
    visitorLocale: text('visitor_locale'),
    visitorTimezone: text('visitor_timezone').notNull(),
    status: text('status', { enum: BOOKING_STATUSES }).notNull().default('confirmed'),
    cancelledBy: text('cancelled_by', { enum: CANCELLED_BY }),
    manageTokenHash: text('manage_token_hash').notNull(),
    googleEventId: text('google_event_id'),
    createdAt: text('created_at').notNull(),
    updatedAt: text('updated_at').notNull(),
  },
  (t) => [index('bookings_page_start_idx').on(t.pageId, t.startAt)],
)

export const bookingPagesRelations = relations(bookingPages, ({ one, many }) => ({
  organization: one(organization, {
    fields: [bookingPages.organizationId],
    references: [organization.id],
  }),
  creator: one(user, {
    fields: [bookingPages.createdBy],
    references: [user.id],
    relationName: 'bookingPageCreator',
  }),
  member: one(user, {
    fields: [bookingPages.memberUserId],
    references: [user.id],
    relationName: 'bookingPageMember',
  }),
  bookings: many(bookings),
}))

export const bookingsRelations = relations(bookings, ({ one }) => ({
  page: one(bookingPages, { fields: [bookings.pageId], references: [bookingPages.id] }),
}))

export type BookingPage = typeof bookingPages.$inferSelect
export type NewBookingPage = typeof bookingPages.$inferInsert
export type Booking = typeof bookings.$inferSelect
export type NewBooking = typeof bookings.$inferInsert

export const SCOPE_TYPES = ['poll', 'booking_page'] as const
export const SUBSCRIPTION_SOURCES = ['creator', 'follow'] as const
export type ScopeType = (typeof SCOPE_TYPES)[number]
export type SubscriptionSource = (typeof SUBSCRIPTION_SOURCES)[number]

/** A user's default notification grid. An absent row means `SYSTEM_DEFAULTS` — the row is only
 * written once someone actually changes something, so most users never have one. */
export const notificationPrefs = sqliteTable('notification_prefs', {
  userId: text('user_id')
    .primaryKey()
    .references(() => user.id, { onDelete: 'cascade' }),
  channels: text('channels', { mode: 'json' }).$type<NotificationGrid>(),
  createdAt: text('created_at').notNull(),
  updatedAt: text('updated_at').notNull(),
})

/**
 * Who is notified about which poll or booking page.
 *
 * Polymorphic on purpose: polls and booking pages share one resolver and one emit path, which is
 * what keeps the two halves of the app from drifting apart. SQLite cannot foreign-key a
 * polymorphic column, so there is no cascade — the poll and booking-page delete paths remove
 * these rows explicitly (`deleteScopeSubscriptions`), and the delivery path skips a scope it can
 * no longer load, so a leaked row is inert rather than dangerous.
 */
export const notificationSubscriptions = sqliteTable(
  'notification_subscriptions',
  {
    scopeType: text('scope_type', { enum: SCOPE_TYPES }).notNull(),
    scopeId: text('scope_id').notNull(),
    userId: text('user_id')
      .notNull()
      .references(() => user.id, { onDelete: 'cascade' }),
    source: text('source', { enum: SUBSCRIPTION_SOURCES }).notNull(),
    /** null = inherit this user's defaults; an object = a per-scope override. */
    channels: text('channels', { mode: 'json' }).$type<NotificationGrid>(),
    createdAt: text('created_at').notNull(),
    updatedAt: text('updated_at').notNull(),
  },
  (t) => [
    primaryKey({ columns: [t.scopeType, t.scopeId, t.userId] }),
    index('notification_subscriptions_scope_idx').on(t.scopeType, t.scopeId),
  ],
)

/** One row per browser/device that has granted push permission. Pruned when the push service
 * reports the endpoint gone (404/410) — see the Phase 2 delivery path. */
export const pushSubscriptions = sqliteTable(
  'push_subscriptions',
  {
    id: text('id').primaryKey(),
    userId: text('user_id')
      .notNull()
      .references(() => user.id, { onDelete: 'cascade' }),
    endpoint: text('endpoint').notNull(),
    p256dh: text('p256dh').notNull(),
    auth: text('auth').notNull(),
    userAgent: text('user_agent'),
    createdAt: text('created_at').notNull(),
    lastSeenAt: text('last_seen_at').notNull(),
  },
  (t) => [
    uniqueIndex('push_subscriptions_endpoint_uidx').on(t.endpoint),
    index('push_subscriptions_user_idx').on(t.userId),
  ],
)

export type NotificationPrefs = typeof notificationPrefs.$inferSelect
export type NotificationSubscription = typeof notificationSubscriptions.$inferSelect
export type PushSubscriptionRow = typeof pushSubscriptions.$inferSelect

export { sql }
