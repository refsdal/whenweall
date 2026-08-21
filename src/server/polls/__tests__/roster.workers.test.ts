import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { applyClaim } from '#/server/polls/claims'
import { buildRosterCsv } from '#/server/polls/roster'
import { createPoll, getPollView } from '#/server/polls/service'
import { makeSignupPoll, makeUser } from '../../../../test/helpers'

describe('buildRosterCsv', () => {
  it('emits a header row, one row per claim, and a zero-claim row for an unclaimed slot', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [2, null] })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [slotA] = view!.options

    await applyClaim(db, pollId, slotA!.id, {
      name: 'Alice',
      email: 'alice@example.com',
      userId: null,
    })

    const csv = await buildRosterCsv(db, pollId, { locale: 'en' })
    const lines = csv
      .replace(/^\uFEFF/, '')
      .split('\r\n')
      .filter(Boolean)

    expect(lines[0]).toBe('slot,capacity,claimed,participant,email')
    expect(lines.some((l) => l.includes('Alice') && l.includes('alice@example.com'))).toBe(true)
    // slotB (unlimited, unclaimed) gets one row with empty participant/email and empty capacity.
    const slotBLabel = 'Slot 2'
    const slotBRow = lines.find((l) => l.startsWith(`${slotBLabel},,0,`))
    expect(slotBRow).toBe(`${slotBLabel},,0,,`)
  })

  it('prefixes the file with a UTF-8 BOM', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [null] })

    const csv = await buildRosterCsv(db, pollId, { locale: 'en' })
    expect(csv.startsWith('\uFEFF')).toBe(true)
  })

  it('quotes fields containing commas, quotes, or newlines per RFC 4180', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await createPoll(db, ownerId, {
      type: 'signup',
      title: 'Sheet',
      timezone: 'Europe/Oslo',
      options: [{ kind: 'text', label: 'Bring "snacks", please', capacity: null }],
    })
    const view = await getPollView(db, pollId, { userId: ownerId })
    const [slot] = view!.options

    await applyClaim(db, pollId, slot!.id, {
      name: 'Bob, Jr.',
      email: 'bob@example.com',
      userId: null,
    })

    const csv = await buildRosterCsv(db, pollId, { locale: 'en' })
    const body = csv.replace(/^\uFEFF/, '')

    expect(body).toContain('"Bring ""snacks"", please"')
    expect(body).toContain('"Bob, Jr."')
  })

  it('leaves capacity empty for unlimited slots and prints the number for capped slots', async () => {
    const db = createDb(env.DB)
    const { id: ownerId } = await makeUser(db)
    const { id: pollId } = await makeSignupPoll(db, ownerId, { capacities: [3, null] })

    const csv = await buildRosterCsv(db, pollId, { locale: 'en' })
    const lines = csv
      .replace(/^\uFEFF/, '')
      .split('\r\n')
      .filter(Boolean)

    expect(lines.some((l) => l.startsWith('Slot 1,3,'))).toBe(true)
    expect(lines.some((l) => l.startsWith('Slot 2,,'))).toBe(true)
  })

  it('throws NOT_FOUND for a missing poll', async () => {
    await expect(
      buildRosterCsv(createDb(env.DB), 'missing12345', { locale: 'en' }),
    ).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})
