import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { requireParticipantAuth, requireSignupPoll } from '#/server/polls/claim-auth'
import { applyClaim } from '#/server/polls/claims'
import { getPollView } from '#/server/polls/service'
import {
  addOrgMember,
  makePoll,
  makeSignupPoll,
  makeUser,
  makeUserWithOrg,
} from '../../../../test/helpers'

describe('requireSignupPoll', () => {
  it('returns the poll organizationId and createdBy for a signup poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null] },
    )

    await expect(requireSignupPoll(db, pollId)).resolves.toMatchObject({
      organizationId: orgId,
      createdBy: ownerId,
    })
  })

  it('throws NOT_FOUND for a missing poll', async () => {
    const db = createDb(env.DB)
    await expect(requireSignupPoll(db, 'missing12345')).rejects.toMatchObject({
      code: 'NOT_FOUND',
    })
  })

  it('throws VALIDATION for a non-signup poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await expect(requireSignupPoll(db, pollId)).rejects.toMatchObject({ code: 'VALIDATION' })
  })
})

describe('requireParticipantAuth', () => {
  async function setup() {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: signedInClaimantId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(
      db,
      { orgId, createdBy: ownerId },
      { capacities: [null, null] },
    )
    const poll = { organizationId: orgId, createdBy: ownerId }
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [slot] = view!.options

    const guestClaim = await applyClaim(db, pollId, slot!.id, { name: 'Guest', userId: null })
    const signedInClaim = await applyClaim(db, pollId, view!.options[1]!.id, {
      name: 'Signed in',
      userId: signedInClaimantId,
    })

    return { db, ownerId, orgId, poll, pollId, signedInClaimantId, guestClaim, signedInClaim }
  }

  it('allows the poll owner', async () => {
    const { db, ownerId, poll, pollId, guestClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, poll, { userId: ownerId }),
    ).resolves.toBeUndefined()
  })

  it('allows an admin who did not create the poll', async () => {
    const { db, orgId, poll, pollId, guestClaim } = await setup()
    const { id: adminId } = await makeUser(db)
    await addOrgMember(db, orgId, adminId, 'admin')
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, poll, { userId: adminId }),
    ).resolves.toBeUndefined()
  })

  it('allows the participant acting as their own signed-in user', async () => {
    const { db, poll, pollId, signedInClaimantId, signedInClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, signedInClaim.participantId, poll, {
        userId: signedInClaimantId,
      }),
    ).resolves.toBeUndefined()
  })

  it('allows a matching edit token', async () => {
    const { db, poll, pollId, guestClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, poll, {
        userId: null,
        editToken: guestClaim.editToken!,
      }),
    ).resolves.toBeUndefined()
  })

  it('throws FORBIDDEN for a same-org member who did not create the poll, with no edit token', async () => {
    const { db, orgId, poll, pollId, guestClaim } = await setup()
    const { id: memberId } = await makeUser(db)
    await addOrgMember(db, orgId, memberId)
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, poll, {
        userId: memberId,
      }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws FORBIDDEN for an unrelated signed-in user with no edit token', async () => {
    const { db, poll, pollId, guestClaim } = await setup()
    const { id: otherUserId } = await makeUser(db)
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, poll, {
        userId: otherUserId,
      }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws FORBIDDEN for a wrong edit token', async () => {
    const { db, poll, pollId, guestClaim } = await setup()
    await expect(
      requireParticipantAuth(db, pollId, guestClaim.participantId, poll, {
        userId: null,
        editToken: 'wrong-token',
      }),
    ).rejects.toMatchObject({ code: 'FORBIDDEN' })
  })

  it('throws NOT_FOUND when the participant does not belong to the poll', async () => {
    const { db, poll, pollId } = await setup()
    const { userId: otherOwnerId, orgId: otherOrgId } = await makeUserWithOrg(db)
    const { id: otherPollId } = await makeSignupPoll(
      db,
      { orgId: otherOrgId, createdBy: otherOwnerId },
      { capacities: [null] },
    )
    const otherView = await getPollView(db, otherPollId, { userId: otherOwnerId })
    const otherClaim = await applyClaim(db, otherPollId, otherView!.options[0]!.id, {
      name: 'Elsewhere',
      userId: null,
    })

    await expect(
      requireParticipantAuth(db, pollId, otherClaim.participantId, poll, { userId: null }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})
