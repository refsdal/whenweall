import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { AppError } from '#/lib/errors'
import { enforceRateLimit } from '#/server/http/rate-limit'

describe('enforceRateLimit', () => {
  it.skipIf(!env.RATE_LIMITER)(
    'allows up to the configured limit then throws RATE_LIMITED',
    async () => {
      for (let i = 0; i < 20; i++) {
        await expect(enforceRateLimit('vote', 'ip1')).resolves.toBeUndefined()
      }

      await expect(enforceRateLimit('vote', 'ip1')).rejects.toMatchObject({
        code: 'RATE_LIMITED',
      })
    },
  )

  it('throws RATE_LIMITED once a fake limiter reports failure', async () => {
    const fakeLimiter = {
      limit: async ({ key }: { key: string }) => ({ success: key !== 'vote:ip2' }),
    }

    await expect(enforceRateLimit('vote', 'ip1', fakeLimiter)).resolves.toBeUndefined()
    await expect(enforceRateLimit('vote', 'ip2', fakeLimiter)).rejects.toThrow(AppError)
    await expect(enforceRateLimit('vote', 'ip2', fakeLimiter)).rejects.toMatchObject({
      code: 'RATE_LIMITED',
    })
  })

  it('is a no-op when explicitly given no limiter', async () => {
    await expect(enforceRateLimit('vote', 'ip3', null)).resolves.toBeUndefined()
  })
})
