import { and, eq, isNull } from 'drizzle-orm'
import type { BatchItem } from 'drizzle-orm/batch'
import type { Db } from '#/server/db/client'
import {
  comments,
  member,
  pollOptions,
  polls,
  participants,
  user,
  type OptionKind,
  type Poll,
  type PollOption,
  type PollType,
} from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { newId, newPollId } from '#/lib/ids'
import { bestOptionId, scoreOptions } from '#/lib/scoring'
import { canManageContent, type OrgRole } from '#/server/auth/org'
import { chunkedInsert } from '#/server/db/chunked-insert'
import { countClaims } from './claims'
import type { OptionInput, UpdatePollInput, CreatePollInput } from './schemas'
import type { PollSummary, PollView } from './viewmodel'

/** The creator/org pair a poll is created under. `createdBy` is nullable on the row (it's
 * cleared if the creator's account is later deleted), but a fresh creation always has one. */
export type PollOwner = { organizationId: string; createdBy: string | null }

type Query = BatchItem<'sqlite'>

type OptionRowFields = {
  kind: OptionKind
  startAt: string | null
  endAt: string | null
  label: string | null
  capacity: number | null
}

/**
 * `fallbackCapacity` is used only when `option.capacity` is omitted: `1` for a brand-new option
 * (the default every signup slot is created with), or the existing row's own capacity when
 * `updatePoll` is re-saving an option the caller didn't touch — an omitted field must retain
 * whatever capacity was already there, not silently reset it to 1.
 */
function optionRowFields(
  option: OptionInput,
  type: PollType,
  fallbackCapacity: number | null = 1,
): OptionRowFields {
  const capacity =
    type === 'signup' ? (option.capacity === undefined ? fallbackCapacity : option.capacity) : null
  if (option.kind === 'date') {
    return { kind: 'date', startAt: option.date, endAt: null, label: null, capacity }
  }
  if (option.kind === 'datetime') {
    return {
      kind: 'datetime',
      startAt: option.startAt,
      endAt: option.endAt ?? null,
      label: null,
      capacity,
    }
  }
  return { kind: 'text', startAt: null, endAt: null, label: option.label, capacity }
}

export async function createPoll(
  db: Db,
  owner: PollOwner,
  input: CreatePollInput,
): Promise<{ id: string }> {
  const id = newPollId()
  const now = new Date().toISOString()

  const optionRows = input.options.map((option, index) => ({
    id: newId(),
    pollId: id,
    position: index,
    ...optionRowFields(option, input.type),
  }))

  await db.batch([
    db.insert(polls).values({
      id,
      organizationId: owner.organizationId,
      createdBy: owner.createdBy,
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
      allowIfNeedBe: input.type === 'signup' ? false : (input.allowIfNeedBe ?? true),
      notifyOnVote: true,
      notifyOnComment: true,
      signupMaxClaims: input.signupMaxClaims ?? 1,
      createdAt: now,
      updatedAt: now,
    }),
    ...chunkedInsert(db, pollOptions, optionRows),
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
      organization: true,
    },
  })
  if (!poll || poll.deletedAt) return null

  // "isOwner" here means "can manage this poll" (spec §1: creator manages their own content,
  // admin/owner manage everything in the org) — the viewer must be a member of the poll's own
  // org, not just any signed-in user, so a wrong-org member never lights up admin controls.
  const membership =
    viewer.userId !== null
      ? await db.query.member.findFirst({
          where: and(
            eq(member.organizationId, poll.organizationId),
            eq(member.userId, viewer.userId),
          ),
        })
      : null
  const isOwner =
    membership !== null &&
    membership !== undefined &&
    canManageContent({ role: membership.role as OrgRole }, viewer.userId!, poll.createdBy)
  const isSignup = poll.type === 'signup'
  const optionIds = poll.options.map((o) => o.id)
  const allVotes = poll.participants.flatMap((p) => p.votes)
  const scores = isSignup ? {} : scoreOptions(optionIds, allVotes)
  const best = isSignup ? null : bestOptionId(optionIds, scores)

  const claims: PollView['claims'] = {}
  for (const option of poll.options) {
    const count = allVotes.filter((v) => v.optionId === option.id && v.answer === 'yes').length
    claims[option.id] = {
      count,
      capacity: option.capacity,
      full: option.capacity !== null && count >= option.capacity,
    }
  }

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
      signupMaxClaims: poll.signupMaxClaims,
    },
    notifications: isOwner
      ? { notifyOnVote: poll.notifyOnVote, notifyOnComment: poll.notifyOnComment }
      : null,
    owner: { name: poll.organization.name },
    isOwner,
    options: poll.options.map((o) => ({
      id: o.id,
      position: o.position,
      kind: o.kind,
      startAt: o.startAt,
      endAt: o.endAt,
      label: o.label,
      capacity: o.capacity,
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
    claims,
  }
}

export async function listMyPolls(db: Db, organizationId: string): Promise<PollSummary[]> {
  const rows = await db.query.polls.findMany({
    where: and(eq(polls.organizationId, organizationId), isNull(polls.deletedAt)),
    orderBy: (p, { desc }) => [desc(p.createdAt)],
    with: { participants: { with: { votes: true } } },
  })

  return rows.map((p) => ({
    id: p.id,
    title: p.title,
    type: p.type,
    status: p.status,
    deadlineAt: p.deadlineAt,
    participantCount: p.participants.length,
    claimCount: p.participants.reduce(
      (sum, participant) => sum + participant.votes.filter((v) => v.answer === 'yes').length,
      0,
    ),
    createdAt: p.createdAt,
    updatedAt: p.updatedAt,
  }))
}

/** The acting org + role, as `requireOrgMiddleware` produces it. */
export type ActingOrg = { id: string; role: OrgRole }

/**
 * NOT_FOUND when the poll doesn't exist, is soft-deleted, or belongs to a different org (no
 * leaking whether a poll id exists at all outside the caller's own org); FORBIDDEN when it's in
 * the right org but the caller can't manage it (a plain member managing someone else's poll).
 */
export async function requireManagedPoll(
  db: Db,
  pollId: string,
  org: ActingOrg,
  userId: string,
): Promise<Poll> {
  const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
  if (!poll || poll.deletedAt || poll.organizationId !== org.id) throw new AppError('NOT_FOUND')
  if (!canManageContent(org, userId, poll.createdBy)) throw new AppError('FORBIDDEN')
  return poll
}

export async function updatePoll(
  db: Db,
  pollId: string,
  org: ActingOrg,
  userId: string,
  input: Omit<UpdatePollInput, 'pollId'>,
): Promise<void> {
  const poll = await requireManagedPoll(db, pollId, org, userId)
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
  if (input.signupMaxClaims !== undefined) scalarUpdate.signupMaxClaims = input.signupMaxClaims

  const queries: Query[] = [db.update(polls).set(scalarUpdate).where(eq(polls.id, pollId))]

  if (input.options !== undefined) {
    const existing = await db.query.pollOptions.findMany({
      where: eq(pollOptions.pollId, pollId),
    })
    const existingIds = new Set(existing.map((o) => o.id))
    const existingById = new Map(existing.map((o) => [o.id, o]))

    if (poll.type === 'signup') {
      const counts = await countClaims(db, pollId)
      for (const option of input.options) {
        if (!option.id || !existingIds.has(option.id)) continue
        const newCapacity =
          option.capacity === undefined ? existingById.get(option.id)!.capacity : option.capacity
        const count = counts[option.id] ?? 0
        if (newCapacity !== null && newCapacity < count) {
          throw new AppError('CAPACITY_BELOW_CLAIMS')
        }
      }
    }

    const keepIds = new Set<string>()

    input.options.forEach((option, index) => {
      const existingId = option.id && existingIds.has(option.id) ? option.id : undefined
      const id = existingId ?? newId()
      keepIds.add(id)
      const fallbackCapacity = existingId ? existingById.get(existingId)!.capacity : 1
      const fields = optionRowFields(option, poll.type, fallbackCapacity)
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
  org: ActingOrg,
  userId: string,
  status: 'open' | 'closed',
): Promise<void> {
  const poll = await requireManagedPoll(db, pollId, org, userId)
  if (poll.status === 'finalized') throw new AppError('POLL_FINALIZED')
  await db
    .update(polls)
    .set({ status, updatedAt: new Date().toISOString() })
    .where(eq(polls.id, pollId))
}

export async function finalizePoll(
  db: Db,
  pollId: string,
  org: ActingOrg,
  userId: string,
  optionId: string,
): Promise<{
  poll: Poll
  option: PollOption
  recipients: { email: string; name: string; locale: string | null }[]
}> {
  const poll = await requireManagedPoll(db, pollId, org, userId)
  if (poll.type === 'signup') throw new AppError('VALIDATION')
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
  // The creator, if the account is still around — nullable now that ownership lives on the org
  // rather than a single user; there's no fallback recipient (e.g. every org admin) in scope
  // here, same as the graceful skip in `PollRoom`'s digest/deadline notifications.
  const owner = poll.createdBy
    ? await db.query.user.findFirst({ where: eq(user.id, poll.createdBy) })
    : null

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

export async function deletePoll(
  db: Db,
  pollId: string,
  org: ActingOrg,
  userId: string,
): Promise<void> {
  await requireManagedPoll(db, pollId, org, userId)
  const now = new Date().toISOString()
  await db.update(polls).set({ deletedAt: now, updatedAt: now }).where(eq(polls.id, pollId))
}

export async function duplicatePoll(
  db: Db,
  pollId: string,
  org: ActingOrg,
  userId: string,
): Promise<{ id: string }> {
  const original = await requireManagedPoll(db, pollId, org, userId)
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
    capacity: o.capacity,
  }))

  await db.batch([
    db.insert(polls).values({
      id,
      organizationId: org.id,
      createdBy: userId,
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
      signupMaxClaims: original.signupMaxClaims,
      createdAt: now,
      updatedAt: now,
    }),
    ...chunkedInsert(db, pollOptions, optionRows),
  ] as [Query, ...Query[]])

  return { id }
}

export async function updateNotificationPrefs(
  db: Db,
  pollId: string,
  org: ActingOrg,
  userId: string,
  prefs: { notifyOnVote: boolean; notifyOnComment: boolean },
): Promise<void> {
  await requireManagedPoll(db, pollId, org, userId)
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
