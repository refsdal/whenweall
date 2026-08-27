import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { AppError } from '#/lib/errors'
import { applyClaim } from '#/server/polls/claims'
import {
  addOrgMember,
  makeOrg,
  makeParticipant,
  makePoll,
  makeSignupPoll,
  makeUser,
  makeUserWithOrg,
} from '../../../../test/helpers'
import {
  closeExpiredPoll,
  createPoll,
  deletePoll,
  duplicatePoll,
  finalizePoll,
  getPollView,
  listMyPolls,
  requireManagedPoll,
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
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'options',
        title: 'Lunch spot',
        timezone: 'Europe/Oslo',
        options: [
          { kind: 'text', label: 'Pizza' },
          { kind: 'text', label: 'Sushi' },
        ],
      },
    )

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
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'datetime',
        title: 'Offsite',
        timezone: 'Europe/Oslo',
        options: [{ kind: 'date', date: '2026-09-15' }],
      },
    )

    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.options[0]).toMatchObject({ kind: 'date', startAt: '2026-09-15', endAt: null })
  })

  it('maps a datetime option kind to startAt/endAt', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'datetime',
        title: 'Sync',
        timezone: 'Europe/Oslo',
        options: [{ kind: 'datetime', startAt: tomorrowAt('10:00'), endAt: tomorrowAt('11:00') }],
      },
    )

    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.options[0]).toMatchObject({
      kind: 'datetime',
      startAt: tomorrowAt('10:00'),
      endAt: tomorrowAt('11:00'),
    })
  })

  it('applies default settings and notification prefs', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id } = await makePoll(db, { orgId, createdBy: ownerId })
    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.settings).toEqual({
      requireParticipantEmail: false,
      allowComments: true,
      allowIfNeedBe: true,
      signupMaxClaims: 1,
    })
    // `createPoll` subscribes the creator with no override — inheriting their account defaults.
    expect(view?.notifications).toEqual({ channels: null, defaults: null, following: true })
    expect(view?.status).toBe('open')
  })

  it('persists per-option capacity and signupMaxClaims for a signup poll, forcing allowIfNeedBe false', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'signup',
        title: 'Bring a dish',
        timezone: 'Europe/Oslo',
        allowIfNeedBe: true,
        signupMaxClaims: 3,
        options: [
          { kind: 'text', label: 'Starter', capacity: 2 },
          { kind: 'text', label: 'Dessert', capacity: null },
        ],
      },
    )

    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.settings.allowIfNeedBe).toBe(false)
    expect(view?.settings.signupMaxClaims).toBe(3)
    expect(view?.options.map((o) => ({ label: o.label, capacity: o.capacity }))).toEqual([
      { label: 'Starter', capacity: 2 },
      { label: 'Dessert', capacity: null },
    ])
  })

  it('creates a poll with 20 datetime options without exceeding D1 bound-parameter limits', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const options = Array.from({ length: 20 }, (_, i) => ({
      kind: 'datetime' as const,
      startAt: tomorrowAt(String(i).padStart(2, '0')),
    }))
    const { id } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'datetime',
        title: 'Big scheduling poll',
        timezone: 'Europe/Oslo',
        options,
      },
    )

    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.options).toHaveLength(20)
    expect(view?.options.map((o) => o.startAt)).toEqual(options.map((o) => o.startAt))
  })

  it('forces capacity to null and signupMaxClaims to 1 for a non-signup poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'options',
        title: 'Lunch spot',
        timezone: 'Europe/Oslo',
        options: [{ kind: 'text', label: 'Pizza' }],
      },
    )

    const view = await getPollView(db, id, { userId: ownerId })
    expect(view?.options[0]?.capacity).toBeNull()
    expect(view?.settings.signupMaxClaims).toBe(1)
  })
})

describe('getPollView', () => {
  it('reflects isOwner for the owner and null notifications for others, without leaking email', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: otherId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    const asOwner = await getPollView(db, pollId, { userId: ownerId })
    expect(asOwner?.isOwner).toBe(true)
    expect(asOwner?.notifications).toEqual({ channels: null, defaults: null, following: true })

    const asOther = await getPollView(db, pollId, { userId: otherId })
    expect(asOther?.isOwner).toBe(false)
    expect(asOther?.notifications).toBeNull()

    const asAnon = await getPollView(db, pollId, { userId: null })
    expect(asAnon?.isOwner).toBe(false)
    expect(asAnon?.notifications).toBeNull()

    // No org id leak to the public/participant view — only the owning org's name is exposed.
    expect(asOwner?.owner).toEqual({ name: 'Test Org' })
    expect(asOwner?.owner).not.toHaveProperty('id')
  })

  it('reports hasEmail true/false per participant without exposing the email', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
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

    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    await deletePoll(db, pollId, org, ownerId)
    expect(await getPollView(db, pollId, { userId: ownerId })).toBeNull()
  })

  it('exposes a claims map with full flags and empty scores for a signup poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [1, null],
        maxClaims: 2,
      },
    )
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [slot1, slot2] = view0!.options
    await applyClaim(db, pollId, slot1!.id, { name: 'Alice', userId: null })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.claims).toEqual({
      [slot1!.id]: { count: 1, capacity: 1, full: true },
      [slot2!.id]: { count: 0, capacity: null, full: false },
    })
    expect(view?.scores).toEqual({})
    expect(view?.bestOptionId).toBeNull()
  })
})

describe('listMyPolls', () => {
  it('lists only the owner non-deleted polls, newest first, with participantCount', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { userId: otherId, orgId: otherOrgId } = await makeUserWithOrg(db)
    const { id: poll1 } = await makePoll(db, { orgId, createdBy: ownerId }, { title: 'First' })
    await new Promise((r) => setTimeout(r, 5))
    const { id: poll2 } = await makePoll(db, { orgId, createdBy: ownerId }, { title: 'Second' })
    const { id: poll3 } = await makePoll(db, { orgId, createdBy: ownerId }, { title: 'Deleted' })
    await makePoll(db, { orgId: otherOrgId, createdBy: otherId }, { title: 'Not mine' })
    await deletePoll(db, poll3, org, ownerId)

    const view = await getPollView(db, poll1, { userId: ownerId })
    const [opt1] = view!.options
    await makeParticipant(db, poll1, 'Alice', { [opt1!.id]: 'yes' })
    await makeParticipant(db, poll1, 'Bob', { [opt1!.id]: 'no' })

    const list = await listMyPolls(db, orgId)
    expect(list.map((p) => p.title)).toEqual(['Second', 'First'])
    const first = list.find((p) => p.id === poll1)
    expect(first?.participantCount).toBe(2)
    const second = list.find((p) => p.id === poll2)
    expect(second?.participantCount).toBe(0)
  })

  it('reports claimCount as the sum of yes-votes, distinct from participantCount, for a sign-up sheet', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [null, null],
        maxClaims: 2,
      },
    )
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [slot1, slot2] = view0!.options

    // Alice claims both slots (one participant, two claims); Bob claims one.
    const alice = await applyClaim(db, pollId, slot1!.id, { name: 'Alice', userId: null })
    await applyClaim(db, pollId, slot2!.id, { participantId: alice.participantId })
    await applyClaim(db, pollId, slot1!.id, { name: 'Bob', userId: null })

    const list = await listMyPolls(db, orgId)
    const summary = list.find((p) => p.id === pollId)
    expect(summary?.participantCount).toBe(2)
    expect(summary?.claimCount).toBe(3)
  })
})

describe('updatePoll', () => {
  it('is manager-only: FORBIDDEN for a same-org member who did not create it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: memberId } = await makeUser(db)
    await addOrgMember(db, orgId, memberId)
    const memberOrg = { id: orgId, role: 'member' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await expect(
      updatePoll(db, pollId, memberOrg, memberId, { title: 'Hacked' }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('is org-scoped: NOT_FOUND for a user in a different org entirely', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { userId: otherId, orgId: otherOrgId } = await makeUserWithOrg(db)
    const otherOrg = { id: otherOrgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await expect(
      updatePoll(db, pollId, otherOrg, otherId, { title: 'Hacked' }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('removing an option deletes its votes; adding an option keeps positions', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const before = await getPollView(db, pollId, { userId: ownerId })
    const [opt1, opt2] = before!.options
    await makeParticipant(db, pollId, 'Alice', { [opt1!.id]: 'yes', [opt2!.id]: 'no' })

    await updatePoll(db, pollId, org, ownerId, {
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

  it('throws POLL_FINALIZED when editing options on a finalized poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [opt1] = view!.options
    await finalizePoll(db, pollId, org, ownerId, opt1!.id)

    await expect(
      updatePoll(db, pollId, org, ownerId, {
        options: [{ id: opt1!.id, kind: 'datetime', startAt: opt1!.startAt! }],
      }),
    ).rejects.toMatchObject({ code: 'POLL_FINALIZED' })
  })

  it('still allows non-option edits (e.g. title) on a finalized poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const view = await getPollView(db, pollId, { userId: ownerId })
    await finalizePoll(db, pollId, org, ownerId, view!.options[0]!.id)

    await updatePoll(db, pollId, org, ownerId, { title: 'Renamed after finalizing' })

    const after = await getPollView(db, pollId, { userId: ownerId })
    expect(after?.title).toBe('Renamed after finalizing')
  })

  it('changing the deadline updates it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const newDeadline = new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString()

    await updatePoll(db, pollId, org, ownerId, { deadlineAt: newDeadline })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.deadlineAt).toBe(newDeadline)
  })

  it('throws CAPACITY_BELOW_CLAIMS when lowering a slot capacity under its current claim count', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [slot] = view0!.options
    await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })
    await applyClaim(db, pollId, slot!.id, { name: 'Bob', userId: null })

    await expect(
      updatePoll(db, pollId, org, ownerId, {
        options: [{ id: slot!.id, kind: 'text', label: slot!.label!, capacity: 1 }],
      }),
    ).rejects.toMatchObject({ code: 'CAPACITY_BELOW_CLAIMS' })
  })

  it('allows raising a slot capacity to at or above its current claim count', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [1] },
    )
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [slot] = view0!.options
    await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })

    await updatePoll(db, pollId, org, ownerId, {
      options: [{ id: slot!.id, kind: 'text', label: slot!.label!, capacity: 5 }],
    })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.options[0]?.capacity).toBe(5)
  })

  it('keeps a retained option’s existing capacity when the update omits it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [7] },
    )
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [slot] = view0!.options

    // No `capacity` on the retained option — must keep 7, not reset to the create-time default of 1.
    await updatePoll(db, pollId, org, ownerId, {
      options: [{ id: slot!.id, kind: 'text', label: 'Renamed slot' }],
    })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.options[0]?.capacity).toBe(7)
    expect(view?.options[0]?.label).toBe('Renamed slot')
  })

  it('still defaults a brand-new option to capacity 1 when the update omits it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [7] },
    )
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [slot] = view0!.options

    await updatePoll(db, pollId, org, ownerId, {
      options: [
        { id: slot!.id, kind: 'text', label: 'Slot 1', capacity: 7 },
        { kind: 'text', label: 'New slot' },
      ],
    })

    const view = await getPollView(db, pollId, { userId: ownerId })
    const newSlot = view!.options.find((o) => o.label === 'New slot')
    expect(newSlot?.capacity).toBe(1)
  })
})

describe('setPollStatus', () => {
  it('closes then reopens', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await setPollStatus(db, pollId, org, ownerId, 'closed')
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('closed')

    await setPollStatus(db, pollId, org, ownerId, 'open')
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('open')
  })

  it('throws POLL_FINALIZED for a finalized poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const view = await getPollView(db, pollId, { userId: ownerId })
    await finalizePoll(db, pollId, org, ownerId, view!.options[0]!.id)

    await expect(setPollStatus(db, pollId, org, ownerId, 'closed')).rejects.toMatchObject({
      code: 'POLL_FINALIZED',
    })
  })
})

describe('finalizePoll', () => {
  it('sets status + finalizedOptionId, computes recipients (deduped, lower-cased) + owner', async () => {
    const db = createDb(env.DB)
    const { id: ownerId, email: ownerEmail } = await makeUser(db, { name: 'Owner', locale: 'nb' })
    const { id: orgId } = await makeOrg(db, ownerId)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
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

    const result = await finalizePoll(db, pollId, org, ownerId, opt1!.id)

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
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const { id: otherPollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const otherView = await getPollView(db, otherPollId, { userId: ownerId })

    await expect(
      finalizePoll(db, pollId, org, ownerId, otherView!.options[0]!.id),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws CONFLICT when finalizing an already-finalized poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [opt1, opt2] = view!.options
    await finalizePoll(db, pollId, org, ownerId, opt1!.id)

    await expect(finalizePoll(db, pollId, org, ownerId, opt2!.id)).rejects.toMatchObject({
      code: 'CONFLICT',
    })
  })

  it('leaves bestOptionId in the view unaffected by finalization', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const view0 = await getPollView(db, pollId, { userId: ownerId })
    const [opt1, opt2] = view0!.options
    await makeParticipant(db, pollId, 'Alice', { [opt1!.id]: 'yes', [opt2!.id]: 'no' })

    const before = await getPollView(db, pollId, { userId: ownerId })
    expect(before?.bestOptionId).toBe(opt1!.id)

    await finalizePoll(db, pollId, org, ownerId, opt2!.id)

    const after = await getPollView(db, pollId, { userId: ownerId })
    expect(after?.bestOptionId).toBe(opt1!.id)
    expect(after?.finalizedOptionId).toBe(opt2!.id)
  })

  it('throws VALIDATION for a signup poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const view = await getPollView(db, pollId, { userId: ownerId })

    await expect(
      finalizePoll(db, pollId, org, ownerId, view!.options[0]!.id),
    ).rejects.toMatchObject({
      code: 'VALIDATION',
    })
  })
})

describe('deletePoll', () => {
  it('soft deletes: getPollView returns null and listMyPolls excludes it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await deletePoll(db, pollId, org, ownerId)

    expect(await getPollView(db, pollId, { userId: ownerId })).toBeNull()
    expect((await listMyPolls(db, orgId)).some((p) => p.id === pollId)).toBe(false)
  })

  it('deleting twice throws NOT_FOUND', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await deletePoll(db, pollId, org, ownerId)
    await expect(deletePoll(db, pollId, org, ownerId)).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})

describe('duplicatePoll', () => {
  it('creates a new poll with same options, zero participants, and title suffix', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId }, { title: 'Original' })
    const original = await getPollView(db, pollId, { userId: ownerId })
    await makeParticipant(db, pollId, 'Alice', { [original!.options[0]!.id]: 'yes' })

    const { id: copyId } = await duplicatePoll(db, pollId, org, ownerId)
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

  it('duplicates a poll with 30 options without exceeding D1 bound-parameter limits', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await createPoll(
      db,
      { organizationId: orgId, createdBy: ownerId },
      {
        type: 'options',
        title: 'Big options poll',
        timezone: 'Europe/Oslo',
        options: Array.from({ length: 30 }, (_, i) => ({
          kind: 'text' as const,
          label: `Option ${i}`,
        })),
      },
    )

    const { id: copyId } = await duplicatePoll(db, pollId, org, ownerId)

    const copy = await getPollView(db, copyId, { userId: ownerId })
    expect(copy?.options).toHaveLength(30)
  })
})

describe('closeExpiredPoll', () => {
  it('closes an open poll past its deadline and returns true', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const past = new Date(Date.now() - 60_000).toISOString()
    await updatePoll(db, pollId, org, ownerId, { deadlineAt: past })

    const changed = await closeExpiredPoll(db, pollId)
    expect(changed).toBe(true)
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('closed')
  })

  it('closes a poll whose deadline has no milliseconds (string comparison would treat it as still future)', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    // A deadline truncated to whole seconds, set to "now". As a *string* comparison,
    // `poll.deadlineAt > now` reads this as still in the future: "Z" (0x5A) sorts after "."
    // (0x2E), the point where `now`'s own ISO string continues on with milliseconds — e.g.
    // "…T10:00:00Z" > "…T10:00:00.004Z" lexicographically, even though the deadline instant is
    // earlier. `Date.parse` compares them as instants instead, so a deadline that has just
    // passed correctly closes the poll.
    const deadline = `${new Date().toISOString().slice(0, 19)}Z`
    await updatePoll(db, pollId, org, ownerId, { deadlineAt: deadline })

    const changed = await closeExpiredPoll(db, pollId)
    expect(changed).toBe(true)
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('closed')
  })

  it('returns false for a future deadline', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const future = new Date(Date.now() + 60_000).toISOString()
    await updatePoll(db, pollId, org, ownerId, { deadlineAt: future })

    expect(await closeExpiredPoll(db, pollId)).toBe(false)
    expect((await getPollView(db, pollId, { userId: ownerId }))?.status).toBe('open')
  })

  it('returns false for an already-finalized poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const past = new Date(Date.now() - 60_000).toISOString()
    await updatePoll(db, pollId, org, ownerId, { deadlineAt: past })
    const view = await getPollView(db, pollId, { userId: ownerId })
    await finalizePoll(db, pollId, org, ownerId, view!.options[0]!.id)

    expect(await closeExpiredPoll(db, pollId)).toBe(false)
  })
})

describe('requireManagedPoll', () => {
  it('throws NOT_FOUND when missing or in a different org, FORBIDDEN for a same-org non-manager', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: memberId } = await makeUser(db)
    await addOrgMember(db, orgId, memberId)
    const memberOrg = { id: orgId, role: 'member' as const }
    const { userId: otherId, orgId: otherOrgId } = await makeUserWithOrg(db)
    const otherOrg = { id: otherOrgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await expect(requireManagedPoll(db, 'missing12345', org, ownerId)).rejects.toBeInstanceOf(
      AppError,
    )
    await expect(requireManagedPoll(db, 'missing12345', org, ownerId)).rejects.toMatchObject({
      code: 'NOT_FOUND',
    })
    await expect(requireManagedPoll(db, pollId, otherOrg, otherId)).rejects.toMatchObject({
      code: 'NOT_FOUND',
    })
    await expect(requireManagedPoll(db, pollId, memberOrg, memberId)).rejects.toMatchObject({
      code: 'FORBIDDEN',
    })
    await expect(requireManagedPoll(db, pollId, org, ownerId)).resolves.toMatchObject({
      id: pollId,
    })
  })
})

describe('updateNotificationPrefs', () => {
  it("stores the viewer's per-poll override", async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await updateNotificationPrefs(db, pollId, org, ownerId, {
      'response.created': { email: false, push: false },
    })

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.notifications?.channels).toEqual({
      'response.created': { email: false, push: false },
    })
    expect(view?.notifications?.following).toBe(true)
  })

  it('clears the override back to the account defaults', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await updateNotificationPrefs(db, pollId, org, ownerId, {
      'response.created': { email: false, push: false },
    })
    await updateNotificationPrefs(db, pollId, org, ownerId, null)

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.notifications?.channels).toBeNull()
  })

  it('lets a teammate who does not manage the poll follow and tune it', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: mate } = await makeUser(db)
    await addOrgMember(db, orgId, mate)
    const org = { id: orgId, role: 'member' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    // Not following yet: the grid is null but the viewer is a member, so the block is present.
    const before = await getPollView(db, pollId, { userId: mate })
    expect(before?.notifications).toEqual({ channels: null, defaults: null, following: false })

    await updateNotificationPrefs(db, pollId, org, mate, {
      'comment.created': { email: true, push: false },
    })

    const after = await getPollView(db, pollId, { userId: mate })
    expect(after?.notifications?.following).toBe(true)
    expect(after?.notifications?.channels).toEqual({
      'comment.created': { email: true, push: false },
    })
    // The owner's own settings are untouched by the teammate's.
    const asOwner = await getPollView(db, pollId, { userId: ownerId })
    expect(asOwner?.notifications?.channels).toBeNull()
  })
})
