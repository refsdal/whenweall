import { describe, expect, it } from 'vitest'
import { nextSearchSchema, safeNext } from '#/lib/search'

describe('nextSearchSchema', () => {
  it('accepts a same-origin path', () => {
    expect(nextSearchSchema.parse({ next: '/foo' })).toEqual({ next: '/foo' })
  })

  it('accepts a same-origin path with a nested segment and a query string', () => {
    expect(nextSearchSchema.parse({ next: '/dashboard' })).toEqual({ next: '/dashboard' })
    expect(nextSearchSchema.parse({ next: '/p/abc?x=1' })).toEqual({ next: '/p/abc?x=1' })
  })

  it('rejects an absolute URL', () => {
    expect(nextSearchSchema.safeParse({ next: 'https://evil.example' }).success).toBe(false)
  })

  it('rejects a protocol-relative URL', () => {
    expect(nextSearchSchema.safeParse({ next: '//evil.example' }).success).toBe(false)
  })

  it('rejects a backslash-prefixed path', () => {
    expect(nextSearchSchema.safeParse({ next: '/\\evil.example' }).success).toBe(false)
  })

  it('rejects an encoded slash', () => {
    expect(nextSearchSchema.safeParse({ next: '/%2Fevil.example' }).success).toBe(false)
  })

  it('rejects an encoded backslash', () => {
    expect(nextSearchSchema.safeParse({ next: '/%5cevil.example' }).success).toBe(false)
  })

  it('rejects a raw-tab-prefixed path that a browser would strip into a protocol-relative URL', () => {
    expect(nextSearchSchema.safeParse({ next: '/\t/evil.example' }).success).toBe(false)
  })

  it('rejects a raw-newline-prefixed path that a browser would strip into a protocol-relative URL', () => {
    expect(nextSearchSchema.safeParse({ next: '/\n/evil.example' }).success).toBe(false)
  })

  it('rejects an encoded-tab-prefixed path', () => {
    expect(nextSearchSchema.safeParse({ next: '/%09/evil.example' }).success).toBe(false)
  })

  it('treats next as optional', () => {
    expect(nextSearchSchema.parse({})).toEqual({})
  })
})

describe('safeNext', () => {
  it('falls back for a protocol-relative path', () => {
    expect(safeNext('//evil.example')).toBe('/')
  })

  it('falls back for a raw-tab-prefixed path', () => {
    expect(safeNext('/\t/evil.example')).toBe('/')
  })

  it('falls back for a raw-newline-prefixed path', () => {
    expect(safeNext('/\n/evil.example')).toBe('/')
  })

  it('falls back for an encoded-tab-prefixed path', () => {
    expect(safeNext('/%09/evil.example')).toBe('/')
  })

  it('falls back to a custom default when next is undefined', () => {
    expect(safeNext(undefined, '/dashboard')).toBe('/dashboard')
  })

  it('passes through a safe path', () => {
    expect(safeNext('/ok')).toBe('/ok')
  })
})
