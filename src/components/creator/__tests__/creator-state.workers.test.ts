import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { createPoll, getPollView } from '#/server/polls/service'
import { createPollSchema } from '#/server/polls/schemas'
import {
  creatorReducer,
  draftToInput,
  initialDraft,
  type CreatorDraft,
} from '#/components/creator/creator-state'
import { makeUserWithOrg } from '../../../../test/helpers'

/**
 * The wizard's payload has to survive the real validator and the real writer, not just a unit
 * test's expectations — these run what the "Create poll" button runs.
 */
function futureDate(daysAhead: number): string {
  const d = new Date(Date.now() + daysAhead * 24 * 60 * 60 * 1000)
  return d.toISOString().slice(0, 10)
}

describe('draftToInput against the real createPoll', () => {
  it('creates a date poll with an all-day option and two time slots', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)

    const dayOne = futureDate(7)
    const dayTwo = futureDate(8)
    let draft: CreatorDraft = { ...initialDraft('Europe/Oslo'), title: '  Team lunch  ' }
    draft = creatorReducer(draft, { type: 'toggleDate', date: dayOne })
    draft = creatorReducer(draft, { type: 'toggleDate', date: dayTwo })
    draft = creatorReducer(draft, { type: 'addSlot', date: dayTwo, start: '09:00', end: '10:30' })
    draft = creatorReducer(draft, { type: 'addSlot', date: dayTwo, start: '18:00', end: null })

    const input = draftToInput(draft)
    expect(createPollSchema.safeParse(input).success).toBe(true)

    const { id } = await createPoll(db, { organizationId: orgId, createdBy: ownerId }, input)
    const view = await getPollView(db, id, { userId: ownerId })

    expect(view?.title).toBe('Team lunch')
    expect(view?.timezone).toBe('Europe/Oslo')
    expect(view?.options.map((o) => o.kind)).toEqual(['date', 'datetime', 'datetime'])
    expect(view?.options[0]?.startAt).toBe(dayOne)
  })

  it('creates an options poll from trimmed text lines', async () => {
    const db = createDb(env.DB)
    const { userId: ownerId, orgId } = await makeUserWithOrg(db)

    let draft: CreatorDraft = {
      ...initialDraft('Europe/Oslo'),
      type: 'options',
      title: 'Friday dinner',
      description: '  ',
    }
    draft = creatorReducer(draft, {
      type: 'setTextOptions',
      options: [{ label: '  Pizza ' }, { label: '' }, { label: 'Sushi' }],
    })

    const input = draftToInput(draft)
    expect(createPollSchema.safeParse(input).success).toBe(true)

    const { id } = await createPoll(db, { organizationId: orgId, createdBy: ownerId }, input)
    const view = await getPollView(db, id, { userId: ownerId })

    expect(view?.description).toBeNull()
    expect(view?.options.map((o) => o.label)).toEqual(['Pizza', 'Sushi'])
  })
})
