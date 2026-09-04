import { api } from '#/api/client'
import type { PublicConfig } from '#/api/types'

export type { PublicConfig }

/** Config the client needs before it can render auth UI: the Turnstile site key (public by
 * design), and whether Google/OIDC sign-in is configured. Ports getPublicConfig
 * (config.functions.ts) against the Go backend's `GET /api/v1/config`
 * (internal/polls/handlers.go's handleConfig) — unauthenticated, no envelope wrapping needed. */
export function getPublicConfig(): Promise<PublicConfig> {
  return api<PublicConfig>('GET', '/api/v1/config')
}
