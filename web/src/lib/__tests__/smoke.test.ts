import { describe, expect, it } from 'vitest'

describe('unit project', () => {
  it('runs in jsdom', () => {
    expect(typeof document).toBe('object')
  })
})
