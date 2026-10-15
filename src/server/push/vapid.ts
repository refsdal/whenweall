/**
 * VAPID application-server authentication — RFC 8292, WebCrypto only.
 *
 * Every push request carries a short-lived ES256 JWT signed by the application server's private
 * key, plus that key's public half. The push service checks the pair, which is what stops anyone
 * who has scraped a subscription endpoint from sending to it.
 */
import { base64UrlDecode, base64UrlEncode } from './encrypt'

const encoder = new TextEncoder()

/** RFC 8292 caps a token at 24h. Kept well under so clock skew between us and the push service
 * can never produce an already-expired token. */
const TOKEN_TTL_SECONDS = 12 * 60 * 60

export type VapidKeys = {
  /** Base64url uncompressed P-256 point. Also sent to the browser as `applicationServerKey`. */
  publicKey: string
  /** Base64url 32-byte scalar. A secret — never leaves the worker. */
  privateKey: string
}

function jsonToBase64Url(value: unknown): string {
  return base64UrlEncode(encoder.encode(JSON.stringify(value)))
}

async function importSigningKey(keys: VapidKeys): Promise<CryptoKey> {
  const publicKey = base64UrlDecode(keys.publicKey)
  return crypto.subtle.importKey(
    'jwk',
    {
      kty: 'EC',
      crv: 'P-256',
      d: keys.privateKey,
      x: base64UrlEncode(publicKey.slice(1, 33)),
      y: base64UrlEncode(publicKey.slice(33, 65)),
      ext: true,
    },
    { name: 'ECDSA', namedCurve: 'P-256' },
    false,
    ['sign'],
  )
}

/**
 * Builds the `Authorization` header for one push request.
 *
 * `audience` is the *origin* of the subscription endpoint, not the full URL — a token minted for
 * `https://fcm.googleapis.com` is valid for every FCM endpoint, so one token per push service per
 * batch is enough rather than one per subscriber.
 *
 * `subject` must be a `mailto:` or `https:` URL the push service can use to contact us about
 * misbehaviour; several services reject tokens without one.
 *
 * `now` is injectable so a test can assert the expiry rather than sleep for it.
 */
export async function createVapidAuthHeader(
  keys: VapidKeys,
  audience: string,
  subject: string,
  now: number = Date.now(),
): Promise<string> {
  const header = jsonToBase64Url({ typ: 'JWT', alg: 'ES256' })
  const claims = jsonToBase64Url({
    aud: audience,
    exp: Math.floor(now / 1000) + TOKEN_TTL_SECONDS,
    sub: subject,
  })
  const signingInput = `${header}.${claims}`

  // WebCrypto's ECDSA output is already the raw r||s pair JOSE wants — no DER unwrapping, which
  // is the step Node-based implementations need and most often get wrong.
  const signature = new Uint8Array(
    await crypto.subtle.sign(
      { name: 'ECDSA', hash: 'SHA-256' },
      await importSigningKey(keys),
      encoder.encode(signingInput) as BufferSource,
    ),
  )

  return `vapid t=${signingInput}.${base64UrlEncode(signature)}, k=${keys.publicKey}`
}

/** The origin a VAPID token must be scoped to for a given subscription endpoint. */
export function audienceFor(endpoint: string): string {
  return new URL(endpoint).origin
}

/**
 * Generates a VAPID keypair.
 *
 * Run once, then `wrangler secret put VAPID_PRIVATE_KEY` and put the public half in
 * `wrangler.jsonc` — it is not a secret and has to reach the browser. Rotating the pair
 * invalidates every existing subscription, so it is effectively write-once.
 */
export async function generateVapidKeys(): Promise<VapidKeys> {
  const pair = (await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, [
    'sign',
    'verify',
  ])) as CryptoKeyPair
  const publicKey = new Uint8Array(await crypto.subtle.exportKey('raw', pair.publicKey))
  const jwk = await crypto.subtle.exportKey('jwk', pair.privateKey)

  return { publicKey: base64UrlEncode(publicKey), privateKey: jwk.d! }
}
