import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { makePoll, makeSignupPoll, makeUser, makeUserWithOrg } from '../../../../test/helpers'
import { applyClaim, countClaims, removeClaim } from '#/server/polls/claims'
import { getPollView, setPollStatus, updatePoll } from '#/server/polls/service'

async function signupOptions(db: ReturnType<typeof createDb>, pollId: string, ownerId: string) {
  const view = await getPollView(db, pollId, { userId: ownerId })
  return view!.options
}

describe('applyClaim', () => {
  it('creates a guest participant and returns a 43-char editToken', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    const result = await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })

    expect(result.created).toBe(true)
    expect(result.editToken).toHaveLength(43)
    expect(result.claimedOptionIds).toEqual([slot!.id])

    const view = await getPollView(db, pollId, { userId: ownerId })
    const alice = view?.participants.find((p) => p.id === result.participantId)
    expect(alice?.votes).toEqual({ [slot!.id]: 'yes' })
  })

  it('returns a null editToken for a signed-in claimant', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: claimantId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    const result = await applyClaim(db, pollId, slot!.id, {
      name: 'Voter',
      userId: claimantId,
    })

    expect(result.editToken).toBeNull()
  })

  it('claims an additional slot for an existing participant via participantId', async () => {
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
    const [slot1, slot2] = await signupOptions(db, pollId, ownerId)

    const first = await applyClaim(db, pollId, slot1!.id, { name: 'Alice', userId: null })
    const second = await applyClaim(db, pollId, slot2!.id, { participantId: first.participantId })

    expect(second.participantId).toBe(first.participantId)
    expect(second.created).toBe(false)
    expect(second.claimedOptionIds.sort()).toEqual([slot1!.id, slot2!.id].sort())
  })

  it('is idempotent: claiming an already-claimed slot is a no-op', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [1] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    const first = await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })
    expect(first.changed).toBe(true)
    const again = await applyClaim(db, pollId, slot!.id, { participantId: first.participantId })

    expect(again.participantId).toBe(first.participantId)
    expect(again.claimedOptionIds).toEqual([slot!.id])
    expect(again.changed).toBe(false)

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.claims[slot!.id]?.count).toBe(1)
  })

  it('reuses the same participant for a signed-in caller claiming twice without participantId', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: claimantId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [null, null],
        maxClaims: 2,
      },
    )
    const [slot1, slot2] = await signupOptions(db, pollId, ownerId)

    const first = await applyClaim(db, pollId, slot1!.id, { name: 'Voter', userId: claimantId })
    expect(first.created).toBe(true)

    const second = await applyClaim(db, pollId, slot2!.id, { name: 'Voter', userId: claimantId })

    expect(second.participantId).toBe(first.participantId)
    expect(second.created).toBe(false)
    expect(second.claimedOptionIds.sort()).toEqual([slot1!.id, slot2!.id].sort())

    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.participants.filter((p) => p.userId === claimantId)).toHaveLength(1)
  })

  it('enforces signupMaxClaims for a signed-in caller who never passes participantId', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: claimantId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [null, null],
        maxClaims: 1,
      },
    )
    const [slot1, slot2] = await signupOptions(db, pollId, ownerId)

    await applyClaim(db, pollId, slot1!.id, { name: 'Voter', userId: claimantId })

    await expect(
      applyClaim(db, pollId, slot2!.id, { name: 'Voter', userId: claimantId }),
    ).rejects.toMatchObject({ code: 'CLAIM_LIMIT_REACHED' })
  })

  it('throws CLAIM_LIMIT_REACHED once maxClaims is reached, then succeeds after it is raised', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [null, null],
        maxClaims: 1,
      },
    )
    const [slot1, slot2] = await signupOptions(db, pollId, ownerId)

    const first = await applyClaim(db, pollId, slot1!.id, { name: 'Alice', userId: null })

    await expect(
      applyClaim(db, pollId, slot2!.id, { participantId: first.participantId }),
    ).rejects.toMatchObject({ code: 'CLAIM_LIMIT_REACHED' })

    await updatePoll(db, pollId, org, ownerId, { signupMaxClaims: 2 })

    const second = await applyClaim(db, pollId, slot2!.id, { participantId: first.participantId })
    expect(second.claimedOptionIds.sort()).toEqual([slot1!.id, slot2!.id].sort())
  })

  it('throws SLOT_FULL once a capacity-1 slot has a claim', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [1],
        maxClaims: 5,
      },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })

    await expect(
      applyClaim(db, pollId, slot!.id, { name: 'Bob', userId: null }),
    ).rejects.toMatchObject({ code: 'SLOT_FULL' })
  })

  it('accepts many claims on an unlimited (null) capacity slot', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [null],
        maxClaims: 5,
      },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    for (const name of ['Alice', 'Bob', 'Carol']) {
      await applyClaim(db, pollId, slot!.id, { name, userId: null })
    }

    const counts = await countClaims(db, pollId)
    expect(counts[slot!.id]).toBe(3)
  })

  it('throws POLL_CLOSED when the poll is not open', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    await expect(
      applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null }),
    ).rejects.toMatchObject({ code: 'POLL_CLOSED' })
  })

  it('throws VALIDATION for a non-signup (datetime) poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [opt1] = view!.options

    await expect(
      applyClaim(db, pollId, opt1!.id, { name: 'Alice', userId: null }),
    ).rejects.toMatchObject({ code: 'VALIDATION' })
  })

  it('throws NOT_FOUND for an option that is not on the poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const { id: otherPollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [otherSlot] = await signupOptions(db, otherPollId, ownerId)

    await expect(
      applyClaim(db, pollId, otherSlot!.id, { name: 'Alice', userId: null }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws NOT_FOUND when the given participantId is not on the poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    await expect(
      applyClaim(db, pollId, slot!.id, { participantId: 'pa_missing' }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })

  it('throws VALIDATION for an empty name', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    await expect(
      applyClaim(db, pollId, slot!.id, { name: '   ', userId: null }),
    ).rejects.toMatchObject({ code: 'VALIDATION' })
  })

  it('throws EMAIL_REQUIRED when the poll requires an email and none is given', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      {
        capacities: [null],
        requireEmail: true,
      },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)

    await expect(
      applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null }),
    ).rejects.toMatchObject({ code: 'EMAIL_REQUIRED' })
  })
})

describe('removeClaim', () => {
  it('removes a claim and returns remaining claimed option ids', async () => {
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
    const [slot1, slot2] = await signupOptions(db, pollId, ownerId)
    const claim = await applyClaim(db, pollId, slot1!.id, { name: 'Alice', userId: null })
    await applyClaim(db, pollId, slot2!.id, { participantId: claim.participantId })

    const result = await removeClaim(db, pollId, slot1!.id, claim.participantId)

    expect(result.remainingOptionIds).toEqual([slot2!.id])
    const view = await getPollView(db, pollId, { userId: ownerId })
    expect(view?.claims[slot1!.id]?.count).toBe(0)
  })

  it('is a no-op when the claim does not exist', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)
    const claim = await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })

    const result = await removeClaim(db, pollId, slot!.id, claim.participantId)
    expect(result.remainingOptionIds).toEqual([])

    const again = await removeClaim(db, pollId, slot!.id, claim.participantId)
    expect(again.remainingOptionIds).toEqual([])
  })

  it('throws POLL_CLOSED when the poll is not open', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)
    const claim = await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    await expect(removeClaim(db, pollId, slot!.id, claim.participantId)).rejects.toMatchObject({
      code: 'POLL_CLOSED',
    })
  })

  it('allows removal on a closed sheet when allowClosed is set (the owner path)', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )
    const [slot] = await signupOptions(db, pollId, ownerId)
    const claim = await applyClaim(db, pollId, slot!.id, { name: 'Alice', userId: null })
    await setPollStatus(db, pollId, org, ownerId, 'closed')

    const result = await removeClaim(db, pollId, slot!.id, claim.participantId, {
      allowClosed: true,
    })

    expect(result.remainingOptionIds).toEqual([])
  })

  it('throws NOT_FOUND for a missing poll and VALIDATION for a non-signup poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await expect(removeClaim(db, 'missing12345', 'x', 'pa_x')).rejects.toMatchObject({
      code: 'NOT_FOUND',
    })
    await expect(removeClaim(db, pollId, 'x', 'pa_x')).rejects.toMatchObject({
      code: 'VALIDATION',
    })
  })
})

describe('countClaims', () => {
  it('returns 0 for slots with no claims and the yes-vote count for claimed ones', async () => {
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
    const [slot1, slot2] = await signupOptions(db, pollId, ownerId)
    await applyClaim(db, pollId, slot1!.id, { name: 'Alice', userId: null })

    const counts = await countClaims(db, pollId)
    expect(counts).toEqual({ [slot1!.id]: 1, [slot2!.id]: 0 })
  })
})
