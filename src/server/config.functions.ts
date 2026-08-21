import { createServerFn } from '@tanstack/react-start'
import { env } from 'cloudflare:workers'

/**
 * Config the client needs before it can render auth UI: the Turnstile site key (public by
 * design), the app's canonical origin, and whether Google sign-in is configured. Loaded once in
 * the root route's `beforeLoad` so every page can read it from route context instead of each
 * component re-fetching it.
 */
export const getPublicConfig = createServerFn({ method: 'GET' }).handler(() => ({
  turnstileSiteKey: env.TURNSTILE_SITE_KEY,
  appUrl: env.APP_URL,
  googleEnabled: Boolean(env.GOOGLE_CLIENT_ID && env.GOOGLE_CLIENT_SECRET),
}))

export type PublicConfig = Awaited<ReturnType<typeof getPublicConfig>>
