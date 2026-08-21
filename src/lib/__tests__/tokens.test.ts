import { describe, expect, it } from 'vitest'
import { generateToken, hashToken, verifyToken } from '#/lib/tokens'
describe('tokens', () => {
  it('generates 43-char base64url tokens', () =>
    expect(generateToken()).toMatch(/^[A-Za-z0-9_-]{43}$/))
  it('hash is deterministic sha-256 hex and verifies', async () => {
    const t = generateToken()
    const h = await hashToken(t)
    expect(h).toMatch(/^[0-9a-f]{64}$/)
    expect(await hashToken(t)).toBe(h)
    expect(await verifyToken(t, h)).toBe(true)
    expect(await verifyToken(generateToken(), h)).toBe(false)
    expect(await verifyToken(t, null)).toBe(false)
  })
})
