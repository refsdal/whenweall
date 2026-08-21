import { describe, expect, it } from 'vitest'
import { AppError, errorCode } from '#/lib/errors'
describe('errors', () => {
  it('round-trips codes through Error messages', () => {
    expect(errorCode(new AppError('FORBIDDEN'))).toBe('FORBIDDEN')
    expect(errorCode(new Error('POLL_CLOSED'))).toBe('POLL_CLOSED')
    expect(errorCode(new Error('boom'))).toBeNull()
    expect(errorCode('x')).toBeNull()
  })
})
