import { env } from 'cloudflare:workers'
import { AppError } from '#/lib/errors'

const SITEVERIFY_URL = 'https://challenges.cloudflare.com/turnstile/v0/siteverify'

export async function verifyTurnstile(
  token: string | undefined,
  remoteIp?: string,
): Promise<boolean> {
  if (!token) return false

  try {
    const body = new URLSearchParams({ secret: env.TURNSTILE_SECRET_KEY, response: token })
    if (remoteIp) body.set('remoteip', remoteIp)

    const res = await fetch(SITEVERIFY_URL, {
      method: 'POST',
      headers: { 'content-type': 'application/x-www-form-urlencoded' },
      body,
    })
    const data = (await res.json()) as { success: boolean }
    return data.success === true
  } catch (err) {
    console.error('turnstile verification failed', err)
    return false
  }
}

export async function requireTurnstile(token: string | undefined): Promise<void> {
  const ok = await verifyTurnstile(token)
  if (!ok) throw new AppError('CAPTCHA_FAILED')
}
