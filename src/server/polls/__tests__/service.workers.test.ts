import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { AppError } from '#/lib/errors'
import { makeParticipant, makePoll, makeUser } from '../../../../test/helpers'
import {
  closeExpiredPoll,
  createPoll,
  deletePoll,
  duplicatePoll,
  finalizePoll,
  getPollView,
  listMyPolls,
  requireOwnedPoll,
  setPollStatus,
  updateNotificationPrefs,
  updatePoll,
} from '#/server/polls/service'

function tomorrowAt(hour: string): string {
  const d = new Date(Date.now() + 24 * 60 * 60 * 1000)
  return `${d.toISOString().slice(0, 10)}T${hour}:00:00.000Z`
}

describe('createPoll', () => {
  it('stores options in order with kinds, and returns a 12-char id', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id } = await createPoll(db, ownerId, {
      type: 'options',
      title: 'Lunch spot',
      timezone: 'Europe/Oslo',
      options: [
        { kind: 'text', label: 'Pizza' },
        { kind: 'text', label: 'Sushi' },
      ],
    })

    expect(id).toHaveLength(12)

    const view = await getPollView(db, id, { userId: ownerId })
    expect(
      view?.options.map((o) => ({ kind: o.kind, label: o.label, position: o.position })),
    ).toEqual([
      { kind: 'text', label: 'Pizza', position: 0 },
      { kind: 'text', label: 'Sushi', position: 1 },
    ])
  })

  it('maps a date option kind to startAt as YYYY-MM-DD', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id } = await createPoll(db, ownerId, {
      type: 'datetime',
      title: 'Offsite',
      timezone: 'Europe/Oslo',
      options: [{ kind: 'date', date: '2026-09-15' }],
    })

    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.options[0]).toMatchObject({ kind: 'date', startAt: '2026-09-15', endAt: null })
  })

  it('maps a datetime option kind to startAt/endAt', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id } = await createPoll(db, ownerId, {
      type: 'datetime',
      title: 'Sync',
      timezone: 'Europe/Oslo',
      options: [{ kind: 'datetime', startAt: tomorrowAt('10:00'), endAt: tomorrowAt('11:00') }],
    })

    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.options[0]).toMatchObject({
      kind: 'datetime',
      startAt: tomorrowAt('10:00'),
      endAt: tomorrowAt('11:00'),
    })
  })

  it('applies default settings and notification prefs', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id } = await makePoll(db, ownerId)
    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.settings).toEqual({
      requireParticipantEmail: false,
      allowComments: true,
      allowIfNeedBe: true,
    })
    expect(view?.notifications).toEqual({ notifyOnVote: true, notifyOnComment: true })
    expect(view?.status).toBe('open')
  })
})

describe('getPollView', () => {
  it('reflects isOwner for the owner and null notifications for others, without leaking email', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    const asOwner = await getPollView(db, pollId, { userId: ownerId })
    expect(asOwner?.isOwner).toBe(true)
    expect(asOwner?.notifications).toEqual({ notifyOnVote: true, notifyOnComment: true })

    const asOther = await getPollView(db, pollId, { userId: otherId })
    expect(asOther?.isOwner).toBe(false)
    expect(asOther?.notifications).toBeNull()

    const asAnon = await getPollView(db, pollId, { userId: null })
    expect(asAnon?.isOwner).toBe(false)
    expect(asAnon?.notifications).toBeNull()

    expect(asOwner?.owner).toEqual({ id: ownerId, name: 'Test User' })
  })

  it('reports hasEmail true/false per participant without exposing the email', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [opt1, opt2] = view0!.options
    const withEmail = await makeParticipant(
      db,
      pollId,
      'Alice',
      { [opt1!.id]: 'yes' },
      { email: 'alice@example.com' },
    )
    const withoutEmail = await makeParticipant(db, pollId, 'Bob', { [opt2!.id]: 'no' })

    const view = await getPollView(db, pollId, { userId: ownerId })
    const alice = view?.participants.find((p) => p.id === withEmail.id)
    const bob = view?.participants.find((p) => p.id === withoutEmail.id)
    expect(alice?.hasEmail).toBe(true)
    expect(bob?.hasEmail).toBe(false)
    expect(JSON.stringify(view)).not.toContain('alice@example.com')
    expect(alice?.votes).toEqual({ [opt1!.id]: 'yes' })
  })

  it('returns null when the poll is missing or soft-deleted', async () => {
    const db = createDb(env.DB)
    expect(await getPollView(db, 'missing12345', { userId: null })).toBeNull()

    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    await deletePoll(db, pollId, ownerId)
    expect(await getPollView(db, pollId, { userId: ownerId })).toBeNull()
  })
})

describe('listMyPolls', () => {
  it('lists only the owner non-deleted polls, newest first, with participantCount', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: poll1 } = await makePoll(db, ownerId, { title: 'First' })
    await new Promise((r) => setTimeout(r, 5))
    const { id: poll2 } = await makePoll(db, ownerId, { title: 'Second' })
    const { id: poll3 } = await makePoll(db, ownerId, { title: 'Deleted' })
    await makePoll(db, otherId, { title: 'Not mine' })
    await deletePoll(db, poll3, ownerId)

    const view = await getPollView(db, poll1, { userId: ownerId })
    const [opt1] = view!.options
    await makeParticipant(db, poll1, 'Alice', { [opt1!.id]: 'yes' })
    await makeParticipant(db, poll1, 'Bob', { [opt1!.id]: 'no' })

    const list = await listMyPolls(db, ownerId)
    expect(list.map((p) => p.title)).toEqual(['Second', 'First'])
    const first = list.find((p) => p.id === poll1)
    expect(first?.participantCount).toBe(2)
    const second = list.find((p) => p.id === poll2)
    expect(second?.participantCount).toBe(0)
  })
})

describe('updatePoll', () => {
  it('is owner-only: FORBIDDEN for a non-owner', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    await expect(updatePoll(db, pollId, otherId, { title: 'Hacked' })).rejects.toMatchObject({
      code: 'FORBIDDEN',
    })
  })

  it('removing an option deletes its votes; adding an option keeps positions', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const before = await getPollView(db, pollId, { userId: ownerId })
    const [opt1, opt2] = before!.options
    await makeParticipant(db, pollId, 'Alice', { [opt1!.id]: 'yes', [opt2!.id]: 'no' })

    await updatePoll(db, pollId, ownerId, {
      options: [
        { id: opt1!.id, kind: 'datetime', startAt: opt1!.startAt! },
        { kind: 'datetime', startAt: tomorrowAt('12:00') },
      ],
    })

    const after = await getPollView(db, pollId, { userId: ownerId })
    expect(after?.options).toHaveLength(2)
    expect(after?.options[0]?.id).toBe(opt1!.id)
    expect(after?.options[0]?.position).toBe(0)
    expect(after?.options[1]?.position).toBe(1)
    expect(after?.options[1]?.id).not.toBe(opt2!.id)

    const alice = after?.participants.find((p) => p.name === 'Alice')
    expect(alice?.votes).toEqual({ [opt1!.id]: 'yes' })
  })

  it('changing the deadline updates it', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const newDeadline = new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString()

    await updatePoll(db, pollId, ownerId, { deadlineAt: newDeadline })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.deadlineAt).toBe(newDeadline)
  })
})

describe('setPollStatus', () => {
  it('closes then reopens', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    await setPollStatus(db, pollId, ownerId, 'closed')
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('closed')

    await setPollStatus(db, pollId, ownerId, 'open')
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('open')
  })

  it('throws POLL_FINALIZED for a finalized poll', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const view = await getPollView(db, pollId, { userId: ownerId })
    await finalizePoll(db, pollId, ownerId, view!.options[0]!.id)

    await expect(setPollStatus(db, pollId, ownerId, 'closed')).rejects.toMatchObject({
      code: 'POLL_FINALIZED',
    })
  })
})

describe('finalizePoll', () => {
  it('sets status + finalizedOptionId, computes recipients (deduped, lower-cased) + owner', async () => {
    const db = createDb(env.DB)
    const { id: ownerId, email: ownerEmail } = await makeUser(db, { name: 'Owner', locale: 'nb' })
    const { id: pollId } = await makePoll(db, ownerId)
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [opt1] = view0!.options

    await makeParticipant(
      db,
      pollId,
      'Alice',
      { [opt1!.id]: 'yes' },
      { email: 'Alice@Example.com' },
    )
    await makeParticipant(db, pollId, 'Alice again', {}, { email: 'alice@example.com' })
    await makeParticipant(db, pollId, 'Bob', {})

    const result = await finalizePoll(db, pollId, ownerId, opt1!.id)

    expect(result.poll.status).toBe('finalized')
    expect(result.poll.finalizedOptionId).toBe(opt1!.id)
    expect(result.option.id).toBe(opt1!.id)

    expect(result.recipients).toHaveLength(2)
    const alice = result.recipients.find((r) => r.email.toLowerCase() === 'alice@example.com')
    expect(alice?.name).toBe('Alice')
    const owner = result.recipients.find((r) => r.email === ownerEmail)
    expect(owner).toEqual({ email: ownerEmail, name: 'Owner', locale: 'nb' })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.status).toBe('finalized')
    expect(view?.finalizedOptionId).toBe(opt1!.id)
  })

  it('throws NOT_FOUND when the option does not belong to the poll', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const { id: otherPollId } = await makePoll(db, ownerId)
    const otherView = await getPollView(db, otherPollId, { userId: ownerId })

    await expect(
      finalizePoll(db, pollId, ownerId, otherView!.options[0]!.id),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws CONFLICT when finalizing an already-finalized poll', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [opt1, opt2] = view!.options
    await finalizePoll(db, pollId, ownerId, opt1!.id)

    await expect(finalizePoll(db, pollId, ownerId, opt2!.id)).rejects.toMatchObject({
      code: 'CONFLICT',
    })
  })

  it('leaves bestOptionId in the view unaffected by finalization', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [opt1, opt2] = view0!.options
    await makeParticipant(db, pollId, 'Alice', { [opt1!.id]: 'yes', [opt2!.id]: 'no' })

    const before = await getPollView(db, pollId, { userId: ownerId })
    expect(before?.bestOptionId).toBe(opt1!.id)

    await finalizePoll(db, pollId, ownerId, opt2!.id)

    const after = await getPollView(db, pollId, { userId: ownerId })
    expect(after?.bestOptionId).toBe(opt1!.id)
    expect(after?.finalizedOptionId).toBe(opt2!.id)
  })
})

describe('deletePoll', () => {
  it('soft deletes: getPollView returns null and listMyPolls excludes it', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    await deletePoll(db, pollId, ownerId)

    expect(await getPollView(db, pollId, { userId: ownerId })).toBeNull()
    expect((await listMyPolls(db, ownerId)).some((p) => p.id === pollId)).toBe(false)
  })

  it('deleting twice throws NOT_FOUND', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    await deletePoll(db, pollId, ownerId)
    await expect(deletePoll(db, pollId, ownerId)).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})

describe('duplicatePoll', () => {
  it('creates a new poll with same options, zero participants, and title suffix', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId, { title: 'Original' })
    const original = await getPollView(db, pollId, { userId: ownerId })
    await makeParticipant(db, pollId, 'Alice', { [original!.options[0]!.id]: 'yes' })

    const { id: copyId } = await duplicatePoll(db, pollId, ownerId)
    expect(copyId).not.toBe(pollId)

    const copy = await getPollView(db, copyId, { userId: ownerId })
    expect(copy?.title).toBe('Original (copy)')
    expect(copy?.status).toBe('open')
    expect(copy?.participants).toHaveLength(0)
    expect(
      copy?.options.map((o) => ({ kind: o.kind, startAt: o.startAt, endAt: o.endAt })),
    ).toEqual(original?.options.map((o) => ({ kind: o.kind, startAt: o.startAt, endAt: o.endAt })))
    expect(copy?.options.map((o) => o.id)).not.toEqual(original?.options.map((o) => o.id))
  })
})

describe('closeExpiredPoll', () => {
  it('closes an open poll past its deadline and returns true', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const past = new Date(Date.now() - 60_000).toISOString()
    await updatePoll(db, pollId, ownerId, { deadlineAt: past })

    const changed = await closeExpiredPoll(db, pollId)
    expect(changed).toBe(true)
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('closed')
  })

  it('returns false for a future deadline', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const future = new Date(Date.now() + 60_000).toISOString()
    await updatePoll(db, pollId, ownerId, { deadlineAt: future })

    expect(await closeExpiredPoll(db, pollId)).toBe(false)
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('open')
  })

  it('returns false for an already-finalized poll', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)
    const past = new Date(Date.now() - 60_000).toISOString()
    await updatePoll(db, pollId, ownerId, { deadlineAt: past })
    const view = await getPollView(db, pollId, { userId: ownerId })
    await finalizePoll(db, pollId, ownerId, view!.options[0]!.id)

    expect(await closeExpiredPoll(db, pollId)).toBe(false)
  })
})

describe('requireOwnedPoll', () => {
  it('throws NOT_FOUND when missing and FORBIDDEN when owned by someone else', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: otherId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    await expect(requireOwnedPoll(db, 'missing12345', ownerId)).rejects.toBeInstanceOf(AppError)
    await expect(requireOwnedPoll(db, 'missing12345', ownerId)).rejects.toMatchObject({
      code: 'NOT_FOUND',
    })
    await expect(requireOwnedPoll(db, pollId, otherId)).rejects.toMatchObject({
      code: 'FORBIDDEN',
    })
    await expect(requireOwnedPoll(db, pollId, ownerId)).resolves.toMatchObject({ id: pollId })
  })
})

describe('updateNotificationPrefs', () => {
  it('updates owner notification prefs', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    await updateNotificationPrefs(db, pollId, ownerId, {
      notifyOnVote: false,
      notifyOnComment: false,
    })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.notifications).toEqual({ notifyOnVote: false, notifyOnComment: false })
  })
})
