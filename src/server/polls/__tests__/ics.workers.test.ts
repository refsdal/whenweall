import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { buildPollIcs } from '#/server/polls/ics'
import { finalizePoll, getPollView } from '#/server/polls/service'
import { makePoll, makeUserWithOrg } from '../../../../test/helpers'

describe('buildPollIcs', () => {
  it('returns null for an open (not yet finalized) poll', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId })

    await expect(buildPollIcs(db, pollId)).resolves.toBeNull()
  })

  it('returns an ics string containing SUMMARY for a poll finalized on a datetime option', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(db, { orgId, createdBy: ownerId }, { title: 'Team sync' })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const optionId = view!.options[0]!.id

    await finalizePoll(db, pollId, org, ownerId, optionId)

    const ics = await buildPollIcs(db, pollId)
    expect(ics).not.toBeNull()
    expect(ics).toContain('BEGIN:VCALENDAR')
    expect(ics).toContain('SUMMARY:Team sync')
  })

  it('returns null for a poll finalized on a text option', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)
    const org = { id: orgId, role: 'owner' as const }
    const { id: pollId } = await makePoll(
      db,
      { orgId, createdBy: ownerId },
      {
        type: 'options',
        options: [
          { kind: 'text', label: 'Pizza' },
          { kind: 'text', label: 'Sushi' },
        ],
      },
    )
    const view = await getPollView(db, pollId, { userId: ownerId })
    const optionId = view!.options[0]!.id

    await finalizePoll(db, pollId, org, ownerId, optionId)

    await expect(buildPollIcs(db, pollId)).resolves.toBeNull()
  })
})
