import { describe, expect, it } from 'vitest'
import { nextSearchSchema } from '#/lib/search'

describe('nextSearchSchema', () => {
  it('accepts a same-origin path', () => {
    expect(nextSearchSchema.parse({ next: '/foo' })).toEqual({ next: '/foo' })
  })

  it('rejects an absolute URL', () => {
    expect(nextSearchSchema.safeParse({ next: 'https://evil.example' }).success).toBe(false)
  })

  it('treats next as optional', () => {
    expect(nextSearchSchema.parse({})).toEqual({})
  })
})
