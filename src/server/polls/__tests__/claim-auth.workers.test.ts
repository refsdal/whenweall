import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { requireParticipantAuth, requireSignupPoll } from '#/server/polls/claim-auth'
import { applyClaim } from '#/server/polls/claims'
import { getPollView } from '#/server/polls/service'
import { makePoll, makeSignupPoll, makeUser } from '../../../../test/helpers'

describe('requireSignupPoll', () => {
  it('returns the poll ownerId for a signup poll', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [null] })

    await expect(requireSignupPoll(db, pollId)).resolves.toMatchObject({ ownerId })
  })

  it('throws NOT_FOUND for a missing poll', async () => {
    const db = createDb(env.DB)
    await expect(requireSignupPoll(db, 'missing12345')).rejects.toMatchObject({
      code: 'NOT_FOUND',
    })
  })

  it('throws VALIDATION for a non-signup poll', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makePoll(db, ownerId)

    await expect(requireSignupPoll(db, pollId)).rejects.toMatchObject({ code: 'VALIDATION' })
  })
})

describe('requireParticipantAuth', () => {
  async function setup() {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: signedInClaimantId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [null, null] })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [slot] = view!.options

    const guestClaim = await applyClaim(db, pollId, slot!.id, { name: 'Guest', userId: null })
    const signedInClaim = await applyClaim(db, pollId, view!.options[1]!.id, {
      name: 'Signed in',
      userId: signedInClaimantId,
    })

    return { db, ownerId, pollId, signedInClaimantId, guestClaim, signedInClaim }
  }

  it('allows the poll owner', async () => {
    const { db, ownerId, pollId, guestClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, ownerId, { userId: ownerId }),
    ).resolves.toBeUndefined()
  })

  it('allows the participant acting as their own signed-in user', async () => {
    const { db, ownerId, pollId, signedInClaimantId, signedInClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, signedInClaim.participantId, ownerId, {
        userId: signedInClaimantId,
      }),
    ).resolves.toBeUndefined()
  })

  it('allows a matching edit token', async () => {
    const { db, ownerId, pollId, guestClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, ownerId, {
        userId: null,
        editToken: guestClaim.editToken!,
      }),
    ).resolves.toBeUndefined()
  })

  it('throws FORBIDDEN for an unrelated signed-in user with no edit token', async () => {
    const { db, ownerId, pollId, guestClaim } = await setup()
    const { id: otherUserId } = await makeUser(db)
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, ownerId, {
        userId: otherUserId,
      }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws FORBIDDEN for a wrong edit token', async () => {
    const { db, ownerId, pollId, guestClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, ownerId, {
        userId: null,
        editToken: 'wrong-token',
      }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws NOT_FOUND when the participant does not belong to the poll', async () => {
    const { db, ownerId, pollId } = await setup()
    const { id: otherOwnerId } = await makeUser(db)
    const { id: otherPollId } = await makeSignupPoll(db, otherOwnerId, { capacities: [null] })
    const otherView = await getPollView(db, otherPollId, { userId: otherOwnerId })
    const otherClaim = await applyClaim(db, otherPollId, otherView!.options[0]!.id, {
      name: 'Elsewhere',
      userId: null,
    })

    await expect(
      requireParticipantAuth(db, pollId, otherClaim.participantId, ownerId, { userId: null }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})
