import { relations, sql } from 'drizzle-orm'
import { index, integer, primaryKey, sqliteTable, text } from 'drizzle-orm/sqlite-core'
import { user } from './auth-schema'

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
    ownerId: text('owner_id')
      .notNull()
      .references(() => user.id, { onDelete: 'cascade' }),
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
    notifyOnVote: bool('notify_on_vote', true),
    notifyOnComment: bool('notify_on_comment', true),
    signupMaxClaims: integer('signup_max_claims').notNull().default(1),
    createdAt: text('created_at').notNull(),
    updatedAt: text('updated_at').notNull(),
    deletedAt: text('deleted_at'),
  },
  (t) => [index('polls_owner_created_idx').on(t.ownerId, t.createdAt)],
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
  owner: one(user, { fields: [polls.ownerId], references: [user.id] }),
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
export { sql }
