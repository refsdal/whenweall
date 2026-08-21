import { describe, expect, it } from 'vitest'
import { requireSessionMiddleware, sessionMiddleware } from '#/server/auth/middleware'

describe('auth middleware', () => {
  it('loads inside workerd (imports getRequestHeaders from start-server-core directly)', () => {
    expect(sessionMiddleware).toBeDefined()
    expect(requireSessionMiddleware).toBeDefined()
  })
})
