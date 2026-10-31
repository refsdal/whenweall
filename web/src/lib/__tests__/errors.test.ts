import { describe, expect, it } from 'vitest'
import { ApiError } from '#/api/client'
import { errorCode, isRemovedNotFound, REMOVED_NOT_FOUND } from '#/lib/errors'

describe('errors', () => {
  it('reads the code off an ApiError', () => {
    expect(errorCode(new ApiError('forbidden', 'forbidden', 403))).toBe('forbidden')
    expect(errorCode(new ApiError('poll_closed', 'this poll is closed', 409))).toBe('poll_closed')
  })

  it('is null for anything that is not an ApiError', () => {
    expect(errorCode(new Error('boom'))).toBeNull()
    expect(errorCode('x')).toBeNull()
    expect(errorCode(null)).toBeNull()
  })

  it('tells a takedown apart from an ordinary not-found', () => {
    expect(isRemovedNotFound(REMOVED_NOT_FOUND)).toBe(true)
    expect(isRemovedNotFound(undefined)).toBe(false)
    expect(isRemovedNotFound({})).toBe(false)
    expect(isRemovedNotFound({ removed: 'yes' })).toBe(false)
  })
})
