import { api } from '#/api/client'

/**
 * The unsubscribe endpoints (internal/httpserver/unsubscribe.go). Unlike every other module here
 * these need no session: the token in the link IS the authorisation, because the people this
 * exists for — a guest who left an address to hear a poll's outcome — have no account to sign
 * into. The server answers with the address the token names, which is what the page shows back
 * so the person can see WHICH mailbox they just changed.
 */
type UnsubscribeResult = { status: 'unsubscribed' | 'subscribed'; email: string }

/** `via=web` marks the row as a confirmed click rather than a provider's one-click POST. */
export function unsubscribe(token: string): Promise<UnsubscribeResult> {
  return api<UnsubscribeResult>('POST', '/api/v1/unsubscribe', undefined, {
    query: { token, via: 'web' },
  })
}

export function resubscribe(token: string): Promise<UnsubscribeResult> {
  return api<UnsubscribeResult>('DELETE', '/api/v1/unsubscribe', undefined, { query: { token } })
}
