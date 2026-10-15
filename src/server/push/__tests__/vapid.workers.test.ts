import { describe, expect, it } from 'vitest'
import { base64UrlDecode } from '#/server/push/encrypt'
import { audienceFor, createVapidAuthHeader, generateVapidKeys } from '#/server/push/vapid'

const encoder = new TextEncoder()

function decodeSegment(segment: string): Record<string, unknown> {
  return JSON.parse(new TextDecoder().decode(base64UrlDecode(segment)))
}

describe('VAPID', () => {
  it('generates a P-256 keypair in the shape the browser and the secret store expect', async () => {
    const keys = await generateVapidKeys()

    // Uncompressed point: 0x04 || X(32) || Y(32).
    const pub = base64UrlDecode(keys.publicKey)
    expect(pub.length).toBe(65)
    expect(pub[0]).toBe(4)
    expect(base64UrlDecode(keys.privateKey).length).toBe(32)
  })

  it('signs a token the matching public key actually verifies', async () => {
    const keys = await generateVapidKeys()

    const header = await createVapidAuthHeader(
      keys,
      'https://fcm.googleapis.com',
      'mailto:hello@whenweall.com',
    )
    const [, token] = /^vapid t=([^,]+), k=(.+)$/.exec(header)!
    const [h, c, sig] = token!.split('.')

    const verified = await crypto.subtle.verify(
      { name: 'ECDSA', hash: 'SHA-256' },
      await crypto.subtle.importKey(
        'raw',
        base64UrlDecode(keys.publicKey) as BufferSource,
        { name: 'ECDSA', namedCurve: 'P-256' },
        false,
        ['verify'],
      ),
      base64UrlDecode(sig!) as BufferSource,
      encoder.encode(`${h}.${c}`) as BufferSource,
    )

    expect(verified).toBe(true)
  })

  it('carries the audience, subject and an expiry inside the RFC 8292 24h cap', async () => {
    const keys = await generateVapidKeys()
    const now = 1_800_000_000_000

    const header = await createVapidAuthHeader(
      keys,
      'https://updates.push.services.mozilla.com',
      'mailto:hello@whenweall.com',
      now,
    )
    const claims = decodeSegment(header.split('t=')[1]!.split(',')[0]!.split('.')[1]!)

    expect(claims.aud).toBe('https://updates.push.services.mozilla.com')
    expect(claims.sub).toBe('mailto:hello@whenweall.com')
    expect(claims.exp).toBeGreaterThan(now / 1000)
    expect((claims.exp as number) - now / 1000).toBeLessThanOrEqual(24 * 60 * 60)
  })

  it('publishes the public key in the header so the push service can check the pair', async () => {
    const keys = await generateVapidKeys()

    const header = await createVapidAuthHeader(keys, 'https://x.example', 'mailto:a@b.c')

    expect(header.endsWith(`, k=${keys.publicKey}`)).toBe(true)
  })

  // One token per push service, not per subscriber — the audience is the endpoint's origin.
  it('scopes the audience to the endpoint origin, not the full URL', () => {
    expect(audienceFor('https://fcm.googleapis.com/fcm/send/abc123?x=1')).toBe(
      'https://fcm.googleapis.com',
    )
  })
})
