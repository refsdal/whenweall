import { and, eq, isNull } from 'drizzle-orm'
import type { BatchItem } from 'drizzle-orm/batch'
import type { Db } from '#/server/db/client'
import {
  comments,
  pollOptions,
  polls,
  participants,
  user,
  type OptionKind,
  type Poll,
  type PollOption,
} from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { newId, newPollId } from '#/lib/ids'
import { bestOptionId, scoreOptions } from '#/lib/scoring'
import type { OptionInput, UpdatePollInput, CreatePollInput } from './schemas'
import type { PollSummary, PollView } from './viewmodel'

type Query = BatchItem<'sqlite'>

type OptionRowFields = {
  kind: OptionKind
  startAt: string | null
  endAt: string | null
  label: string | null
}

function optionRowFields(option: OptionInput): OptionRowFields {
  if (option.kind === 'date') {
    return { kind: 'date', startAt: option.date, endAt: null, label: null }
  }
  if (option.kind === 'datetime') {
    return { kind: 'datetime', startAt: option.startAt, endAt: option.endAt ?? null, label: null }
  }
  return { kind: 'text', startAt: null, endAt: null, label: option.label }
}

export async function createPoll(
  db: Db,
  ownerId: string,
  input: CreatePollInput,
): Promise<{ id: string }> {
  const id = newPollId()
  const now = new Date().toISOString()

  const optionRows = input.options.map((option, index) => ({
    id: newId(),
    pollId: id,
    position: index,
    ...optionRowFields(option),
  }))

  await db.batch([
    db.insert(polls).values({
      id,
      ownerId,
      type: input.type,
      title: input.title,
      description: input.description ?? null,
      location: input.location ?? null,
      timezone: input.timezone,
      status: 'open',
      deadlineAt: input.deadlineAt ?? null,
      finalizedOptionId: null,
      requireParticipantEmail: input.requireParticipantEmail ?? false,
      allowComments: input.allowComments ?? true,
      allowIfNeedBe: input.allowIfNeedBe ?? true,
      notifyOnVote: true,
      notifyOnComment: true,
      createdAt: now,
      updatedAt: now,
    }),
    db.insert(pollOptions).values(optionRows),
  ] as [Query, ...Query[]])

  return { id }
}

export async function getPollView(
  db: Db,
  pollId: string,
  viewer: { userId: string | null },
): Promise<PollView | null> {
  const poll = await db.query.polls.findFirst({
    where: eq(polls.id, pollId),
    with: {
      options: { orderBy: (o, { asc }) => [asc(o.position)] },
      participants: { with: { votes: true } },
      comments: {
        where: isNull(comments.deletedAt),
        orderBy: (c, { asc }) => [asc(c.createdAt)],
      },
      owner: true,
    },
  })
  if (!poll || poll.deletedAt) return null

  const isOwner = viewer.userId !== null && viewer.userId === poll.ownerId
  const optionIds = poll.options.map((o) => o.id)
  const allVotes = poll.participants.flatMap((p) => p.votes)
  const scores = scoreOptions(optionIds, allVotes)
  const best = bestOptionId(optionIds, scores)

  return {
    id: poll.id,
    type: poll.type,
    title: poll.title,
    description: poll.description,
    location: poll.location,
    timezone: poll.timezone,
    status: poll.status,
    deadlineAt: poll.deadlineAt,
    finalizedOptionId: poll.finalizedOptionId,
    createdAt: poll.createdAt,
    settings: {
      requireParticipantEmail: poll.requireParticipantEmail,
      allowComments: poll.allowComments,
      allowIfNeedBe: poll.allowIfNeedBe,
    },
    notifications: isOwner
      ? { notifyOnVote: poll.notifyOnVote, notifyOnComment: poll.notifyOnComment }
      : null,
    owner: { id: poll.owner.id, name: poll.owner.name },
    isOwner,
    options: poll.options.map((o) => ({
      id: o.id,
      position: o.position,
      kind: o.kind,
      startAt: o.startAt,
      endAt: o.endAt,
      label: o.label,
    })),
    participants: poll.participants.map((p) => ({
      id: p.id,
      name: p.name,
      userId: p.userId,
      hasEmail: !!p.email,
      votes: Object.fromEntries(p.votes.map((v) => [v.optionId, v.answer])),
      createdAt: p.createdAt,
    })),
    comments: poll.comments.map((c) => ({
      id: c.id,
      authorName: c.authorName,
      body: c.body,
      createdAt: c.createdAt,
      userId: c.userId,
      participantId: c.participantId,
    })),
    scores,
    bestOptionId: best,
  }
}

export async function listMyPolls(db: Db, ownerId: string): Promise<PollSummary[]> {
  const rows = await db.query.polls.findMany({
    where: and(eq(polls.ownerId, ownerId), isNull(polls.deletedAt)),
    orderBy: (p, { desc }) => [desc(p.createdAt)],
    with: { participants: true },
  })

  return rows.map((p) => ({
    id: p.id,
    title: p.title,
    type: p.type,
    status: p.status,
    deadlineAt: p.deadlineAt,
    participantCount: p.participants.length,
    createdAt: p.createdAt,
    updatedAt: p.updatedAt,
  }))
}

export async function requireOwnedPoll(db: Db, pollId: string, ownerId: string): Promise<Poll> {
  const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')
  if (poll.ownerId !== ownerId) throw new AppError('FORBIDDEN')
  return poll
}

export async function updatePoll(
  db: Db,
  pollId: string,
  ownerId: string,
  input: Omit<UpdatePollInput, 'pollId'>,
): Promise<void> {
  const poll = await requireOwnedPoll(db, pollId, ownerId)
  if (poll.status === 'finalized' && input.options !== undefined) {
    throw new AppError('POLL_FINALIZED')
  }
  const now = new Date().toISOString()

  const scalarUpdate: Partial<typeof polls.$inferInsert> = { updatedAt: now }
  if (input.title !== undefined) scalarUpdate.title = input.title
  if (input.description !== undefined) scalarUpdate.description = input.description ?? null
  if (input.location !== undefined) scalarUpdate.location = input.location ?? null
  if (input.timezone !== undefined) scalarUpdate.timezone = input.timezone
  if (input.deadlineAt !== undefined) scalarUpdate.deadlineAt = input.deadlineAt ?? null
  if (input.requireParticipantEmail !== undefined) {
    scalarUpdate.requireParticipantEmail = input.requireParticipantEmail
  }
  if (input.allowComments !== undefined) scalarUpdate.allowComments = input.allowComments
  if (input.allowIfNeedBe !== undefined) scalarUpdate.allowIfNeedBe = input.allowIfNeedBe

  const queries: Query[] = [db.update(polls).set(scalarUpdate).where(eq(polls.id, pollId))]

  if (input.options !== undefined) {
    const existing = await db.query.pollOptions.findMany({
      where: eq(pollOptions.pollId, pollId),
    })
    const existingIds = new Set(existing.map((o) => o.id))
    const keepIds = new Set<string>()

    input.options.forEach((option, index) => {
      const existingId = option.id && existingIds.has(option.id) ? option.id : undefined
      const id = existingId ?? newId()
      keepIds.add(id)
      const fields = optionRowFields(option)
      if (existingId) {
        queries.push(
          db
            .update(pollOptions)
            .set({ position: index, ...fields })
            .where(eq(pollOptions.id, id)),
        )
      } else {
        queries.push(db.insert(pollOptions).values({ id, pollId, position: index, ...fields }))
      }
    })

    for (const option of existing) {
      if (!keepIds.has(option.id)) {
        queries.push(db.delete(pollOptions).where(eq(pollOptions.id, option.id)))
      }
    }
  }

  await db.batch(queries as [Query, ...Query[]])
}

export async function setPollStatus(
  db: Db,
  pollId: string,
  ownerId: string,
  status: 'open' | 'closed',
): Promise<void> {
  const poll = await requireOwnedPoll(db, pollId, ownerId)
  if (poll.status === 'finalized') throw new AppError('POLL_FINALIZED')
  await db
    .update(polls)
    .set({ status, updatedAt: new Date().toISOString() })
    .where(eq(polls.id, pollId))
}

export async function finalizePoll(
  db: Db,
  pollId: string,
  ownerId: string,
  optionId: string,
): Promise<{
  poll: Poll
  option: PollOption
  recipients: { email: string; name: string; locale: string | null }[]
}> {
  const poll = await requireOwnedPoll(db, pollId, ownerId)
  if (poll.status === 'finalized') throw new AppError('CONFLICT')

  const option = await db.query.pollOptions.findFirst({ where: eq(pollOptions.id, optionId) })
  if (!option || option.pollId !== pollId) throw new AppError('NOT_FOUND')

  const now = new Date().toISOString()
  const [updatedPoll] = await db
    .update(polls)
    .set({ status: 'finalized', finalizedOptionId: optionId, updatedAt: now })
    .where(eq(polls.id, pollId))
    .returning()

  const participantRows = await db.query.participants.findMany({
    where: eq(participants.pollId, pollId),
  })
  const owner = await db.query.user.findFirst({ where: eq(user.id, ownerId) })

  const recipients = new Map<string, { email: string; name: string; locale: string | null }>()
  for (const p of participantRows) {
    if (!p.email) continue
    const key = p.email.toLowerCase()
    if (!recipients.has(key)) {
      recipients.set(key, { email: p.email, name: p.name, locale: p.locale ?? null })
    }
  }
  if (owner) {
    const key = owner.email.toLowerCase()
    if (!recipients.has(key)) {
      recipients.set(key, { email: owner.email, name: owner.name, locale: owner.locale ?? null })
    }
  }

  return { poll: updatedPoll!, option, recipients: [...recipients.values()] }
}

export async function deletePoll(db: Db, pollId: string, ownerId: string): Promise<void> {
  await requireOwnedPoll(db, pollId, ownerId)
  const now = new Date().toISOString()
  await db.update(polls).set({ deletedAt: now, updatedAt: now }).where(eq(polls.id, pollId))
}

export async function duplicatePoll(
  db: Db,
  pollId: string,
  ownerId: string,
): Promise<{ id: string }> {
  const original = await requireOwnedPoll(db, pollId, ownerId)
  const options = await db.query.pollOptions.findMany({
    where: eq(pollOptions.pollId, pollId),
    orderBy: (o, { asc }) => [asc(o.position)],
  })

  const id = newPollId()
  const now = new Date().toISOString()

  const optionRows = options.map((o) => ({
    id: newId(),
    pollId: id,
    position: o.position,
    kind: o.kind,
    startAt: o.startAt,
    endAt: o.endAt,
    label: o.label,
  }))

  await db.batch([
    db.insert(polls).values({
      id,
      ownerId,
      type: original.type,
      title: `${original.title} (copy)`,
      description: original.description,
      location: original.location,
      timezone: original.timezone,
      status: 'open',
      deadlineAt: null,
      finalizedOptionId: null,
      requireParticipantEmail: original.requireParticipantEmail,
      allowComments: original.allowComments,
      allowIfNeedBe: original.allowIfNeedBe,
      notifyOnVote: original.notifyOnVote,
      notifyOnComment: original.notifyOnComment,
      createdAt: now,
      updatedAt: now,
    }),
    db.insert(pollOptions).values(optionRows),
  ] as [Query, ...Query[]])

  return { id }
}

export async function updateNotificationPrefs(
  db: Db,
  pollId: string,
  ownerId: string,
  prefs: { notifyOnVote: boolean; notifyOnComment: boolean },
): Promise<void> {
  await requireOwnedPoll(db, pollId, ownerId)
  await db
    .update(polls)
    .set({
      notifyOnVote: prefs.notifyOnVote,
      notifyOnComment: prefs.notifyOnComment,
      updatedAt: new Date().toISOString(),
    })
    .where(eq(polls.id, pollId))
}

export async function closeExpiredPoll(db: Db, pollId: string): Promise<boolean> {
  const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
  if (!poll || poll.deletedAt) return false
  if (poll.status !== 'open') return false
  if (!poll.deadlineAt) return false

  if (Date.parse(poll.deadlineAt) > Date.now()) return false

  const now = new Date().toISOString()

  await db.update(polls).set({ status: 'closed', updatedAt: now }).where(eq(polls.id, pollId))
  return true
}
