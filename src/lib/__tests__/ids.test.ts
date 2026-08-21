import { describe, expect, it } from 'vitest'
import { newId, newPollId } from '#/lib/ids'
describe('ids', () => {
  it('poll ids are 12 url-safe alphanumerics and unique', () => {
    const ids = new Set(Array.from({ length: 500 }, newPollId))
    expect(ids.size).toBe(500)
    for (const id of ids) expect(id).toMatch(/^[0-9A-Za-z]{12}$/)
  })
  it('row ids are 16 chars', () => expect(newId()).toMatch(/^[0-9A-Za-z_-]{16}$/))
})
