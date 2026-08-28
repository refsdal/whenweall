/**
 * Web Push message encryption — RFC 8291 (aes128gcm), built on WebCrypto only.
 *
 * Hand-rolled rather than taken from a dependency. The canonical `web-push` package is Node-only
 * (it reaches for `crypto`, `http_ece` and `https-proxy-agent`), and every Workers-compatible
 * alternative on npm is young and thinly used — single-digit-thousand weekly downloads, a handful
 * of releases. For ~150 lines of well-specified, test-vector-verifiable crypto behind a paid
 * feature, owning it beats depending on it.
 *
 * The shape is fixed by the RFC:
 *
 *   ECDH(as_private, ua_public) -> shared secret
 *   HKDF(salt=auth_secret, ikm=shared, info="WebPush: info"||0||ua_pub||as_pub) -> IKM
 *   HKDF(salt=random, ikm=IKM, info="Content-Encoding: aes128gcm"||0) -> 16-byte CEK
 *   HKDF(salt=random, ikm=IKM, info="Content-Encoding: nonce"||0)     -> 12-byte nonce
 *   body = header || AES128GCM(plaintext || 0x02)
 */

const encoder = new TextEncoder()

export function base64UrlDecode(value: string): Uint8Array {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/')
  const binary = atob(padded + '='.repeat((4 - (padded.length % 4)) % 4))
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
  return bytes
}

export function base64UrlEncode(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function concat(...parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let offset = 0
  for (const part of parts) {
    out.set(part, offset)
    offset += part.length
  }
  return out
}

async function hkdf(
  salt: Uint8Array,
  ikm: Uint8Array,
  info: Uint8Array,
  length: number,
): Promise<Uint8Array> {
  const key = await crypto.subtle.importKey('raw', ikm as BufferSource, 'HKDF', false, [
    'deriveBits',
  ])
  const bits = await crypto.subtle.deriveBits(
    { name: 'HKDF', hash: 'SHA-256', salt: salt as BufferSource, info: info as BufferSource },
    key,
    length * 8,
  )
  return new Uint8Array(bits)
}

/** An uncompressed P-256 point (0x04 || X || Y) as WebCrypto's raw format. */
async function importPublicKey(raw: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    'raw',
    raw as BufferSource,
    { name: 'ECDH', namedCurve: 'P-256' },
    true,
    [],
  )
}

/**
 * Imports a raw 32-byte P-256 scalar as an ECDH private key.
 *
 * WebCrypto has no "raw" import for private keys, so this goes in as a JWK. The public
 * coordinates are required fields even though only `d` is used for deriveBits, hence the
 * `publicKey` argument — the caller always has the matching public key on hand.
 */
async function importPrivateKey(d: Uint8Array, publicKey: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    'jwk',
    {
      kty: 'EC',
      crv: 'P-256',
      d: base64UrlEncode(d),
      x: base64UrlEncode(publicKey.slice(1, 33)),
      y: base64UrlEncode(publicKey.slice(33, 65)),
      ext: true,
    },
    { name: 'ECDH', namedCurve: 'P-256' },
    false,
    ['deriveBits'],
  )
}

export type PushSubscriptionKeys = {
  /** The subscription endpoint's `p256dh` — the user agent's public key, base64url. */
  p256dh: string
  /** The subscription's `auth` secret, base64url. */
  auth: string
}

/**
 * Encrypts one push message body.
 *
 * `salt` and the ephemeral server keypair are parameters rather than generated inside, so the
 * RFC 8291 test vector can be reproduced exactly. Production callers omit both and get fresh
 * random values, which is what the spec requires — a reused salt with the same keys would leak
 * plaintext.
 */
export async function encryptPushPayload(
  payload: string,
  subscription: PushSubscriptionKeys,
  options: {
    salt?: Uint8Array
    serverKeys?: { publicKey: Uint8Array; privateKey: Uint8Array }
  } = {},
): Promise<Uint8Array> {
  const uaPublic = base64UrlDecode(subscription.p256dh)
  const authSecret = base64UrlDecode(subscription.auth)

  let asPublic: Uint8Array
  let asPrivateKey: CryptoKey
  if (options.serverKeys) {
    asPublic = options.serverKeys.publicKey
    asPrivateKey = await importPrivateKey(options.serverKeys.privateKey, asPublic)
  } else {
    const pair = (await crypto.subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, [
      'deriveBits',
    ])) as CryptoKeyPair
    asPublic = new Uint8Array(await crypto.subtle.exportKey('raw', pair.publicKey))
    asPrivateKey = pair.privateKey
  }

  const salt = options.salt ?? crypto.getRandomValues(new Uint8Array(16))

  const shared = new Uint8Array(
    await crypto.subtle.deriveBits(
      { name: 'ECDH', public: await importPublicKey(uaPublic) },
      asPrivateKey,
      256,
    ),
  )

  // The key-combining step is what binds the ciphertext to this specific subscription: both
  // public keys go into the info string, so a message encrypted for one subscriber cannot be
  // replayed at another.
  const ikm = await hkdf(
    authSecret,
    shared,
    concat(encoder.encode('WebPush: info'), new Uint8Array([0]), uaPublic, asPublic),
    32,
  )

  const cek = await hkdf(
    salt,
    ikm,
    concat(encoder.encode('Content-Encoding: aes128gcm'), new Uint8Array([0])),
    16,
  )
  const nonce = await hkdf(
    salt,
    ikm,
    concat(encoder.encode('Content-Encoding: nonce'), new Uint8Array([0])),
    12,
  )

  const key = await crypto.subtle.importKey('raw', cek as BufferSource, 'AES-GCM', false, [
    'encrypt',
  ])
  // 0x02 is the RFC 8188 final-record delimiter. Everything is sent as one record, so it is
  // always the last one.
  const ciphertext = new Uint8Array(
    await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: nonce as BufferSource },
      key,
      concat(encoder.encode(payload), new Uint8Array([2])) as BufferSource,
    ),
  )

  // RFC 8188 header: salt(16) || record size(4, big-endian) || key id length(1) || key id.
  // The key id is the server's public key, which is how the user agent knows what to ECDH with.
  const recordSize = new Uint8Array(4)
  new DataView(recordSize.buffer).setUint32(0, 4096, false)

  return concat(salt, recordSize, new Uint8Array([asPublic.length]), asPublic, ciphertext)
}
