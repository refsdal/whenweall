import { describe, expect, it } from 'vitest'
import { base64UrlDecode, base64UrlEncode, encryptPushPayload } from '#/server/push/encrypt'

/**
 * The published example from RFC 8291 §5. Reproducing it byte for byte is the only convincing
 * evidence that a hand-rolled implementation of this is correct — the failure mode of getting
 * the HKDF info strings or the record header subtly wrong is a payload that every push service
 * accepts and no browser can decrypt.
 */
const VECTOR = {
  plaintext: 'When I grow up, I want to be a watermelon',
  uaPublic:
    'BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4',
  authSecret: 'BTBZMqHH6r4Tts7J_aSIgg',
  asPublic:
    'BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8',
  asPrivate: 'yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw',
  salt: 'DGv6ra1nlYgDCS1FRnbzlw',
  expected:
    'DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN',
}

describe('encryptPushPayload', () => {
  it('reproduces the RFC 8291 §5 test vector exactly', async () => {
    const body = await encryptPushPayload(
      VECTOR.plaintext,
      { p256dh: VECTOR.uaPublic, auth: VECTOR.authSecret },
      {
        salt: base64UrlDecode(VECTOR.salt),
        serverKeys: {
          publicKey: base64UrlDecode(VECTOR.asPublic),
          privateKey: base64UrlDecode(VECTOR.asPrivate),
        },
      },
    )

    expect(base64UrlEncode(body)).toBe(VECTOR.expected)
  })

  it('emits the RFC 8188 header: salt, record size, then the server public key', async () => {
    const body = await encryptPushPayload(
      VECTOR.plaintext,
      { p256dh: VECTOR.uaPublic, auth: VECTOR.authSecret },
      {
        salt: base64UrlDecode(VECTOR.salt),
        serverKeys: {
          publicKey: base64UrlDecode(VECTOR.asPublic),
          privateKey: base64UrlDecode(VECTOR.asPrivate),
        },
      },
    )

    expect(base64UrlEncode(body.slice(0, 16))).toBe(VECTOR.salt)
    expect(new DataView(body.buffer, body.byteOffset + 16, 4).getUint32(0, false)).toBe(4096)
    expect(body[20]).toBe(65) // uncompressed P-256 point length
    expect(base64UrlEncode(body.slice(21, 86))).toBe(VECTOR.asPublic)
  })

  // A reused salt with the same keypair would reuse the AES-GCM nonce, which leaks plaintext.
  // Production callers pass neither, so both must be fresh on every call.
  it('generates a fresh salt and keypair when none are supplied', async () => {
    const sub = { p256dh: VECTOR.uaPublic, auth: VECTOR.authSecret }

    const a = await encryptPushPayload(VECTOR.plaintext, sub)
    const b = await encryptPushPayload(VECTOR.plaintext, sub)

    expect(base64UrlEncode(a.slice(0, 16))).not.toBe(base64UrlEncode(b.slice(0, 16)))
    expect(base64UrlEncode(a.slice(21, 86))).not.toBe(base64UrlEncode(b.slice(21, 86)))
    expect(base64UrlEncode(a)).not.toBe(base64UrlEncode(b))
  })
})
