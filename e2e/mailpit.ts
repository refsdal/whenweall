import { expect, type APIRequestContext } from '@playwright/test'
import { APP_URL, MAILPIT_HTTP_PORT } from './e2e-env'

/**
 * Reads the Mailpit inbox the e2e server delivers into (SMTP :1026 → HTTP API :8026, both modes).
 *
 * Endpoints (Mailpit v1 API, pinned image in e2e-env.ts):
 *   GET  /api/v1/search?query=<q>&limit=<n>  → { messages: [MailpitSummary, …] } newest first
 *   GET  /api/v1/message/{ID}                → MailpitMessage (decoded Text + HTML bodies)
 * The search syntax `to:"addr@example.com"` scopes to one recipient. Both shapes were checked
 * against Mailpit's swagger.json while planning; if a future bump changes them, this file is the
 * only place to fix.
 *
 * Every mail leaves the Go server through the scheduled_jobs worker (5s poll, NOTIFY-woken), so
 * "the mail exists" is always an eventually-true assertion: use `waitForMail`/`expect.poll`, never
 * a one-shot `searchMail` right after the action that triggers it.
 */
export const MAILPIT_API = `http://localhost:${MAILPIT_HTTP_PORT}/api/v1`

export type MailpitAddress = { Name: string; Address: string }

export type MailpitSummary = {
  ID: string
  MessageID: string
  From: MailpitAddress
  To: MailpitAddress[]
  Subject: string
  Snippet: string
  Created: string
}

export type MailpitMessage = MailpitSummary & { Text: string; HTML: string }

/** Every message addressed to `to`, newest first, optionally narrowed by subject. */
export async function searchMail(
  request: APIRequestContext,
  to: string,
  subject?: RegExp,
): Promise<MailpitSummary[]> {
  const response = await request.get(`${MAILPIT_API}/search`, {
    params: { query: `to:"${to}"`, limit: 50 },
  })
  if (!response.ok()) {
    throw new Error(
      `Mailpit search responded ${response.status()} — is the Mailpit container up on :${MAILPIT_HTTP_PORT}?`,
    )
  }
  const { messages } = (await response.json()) as { messages: MailpitSummary[] }
  return subject ? messages.filter((message) => subject.test(message.Subject)) : messages
}

export async function countMail(
  request: APIRequestContext,
  to: string,
  subject?: RegExp,
): Promise<number> {
  return (await searchMail(request, to, subject)).length
}

export async function readMail(request: APIRequestContext, id: string): Promise<MailpitMessage> {
  const response = await request.get(`${MAILPIT_API}/message/${id}`)
  if (!response.ok()) {
    throw new Error(`Mailpit message ${id} responded ${response.status()}`)
  }
  return (await response.json()) as MailpitMessage
}

/**
 * Polls until at least `minCount` (default 1) messages to `to` (matching `subject`, if given) exist,
 * then returns the newest one in full.
 */
export async function waitForMail(
  request: APIRequestContext,
  to: string,
  opts: { subject?: RegExp; minCount?: number; timeout?: number } = {},
): Promise<MailpitMessage> {
  const minCount = opts.minCount ?? 1
  let matches: MailpitSummary[] = []
  await expect
    .poll(
      async () => {
        matches = await searchMail(request, to, opts.subject)
        return matches.length
      },
      {
        timeout: opts.timeout ?? 30_000,
        message: `waiting for ${minCount} mail(s) to ${to}${opts.subject ? ` with subject ${opts.subject}` : ''}`,
      },
    )
    .toBeGreaterThanOrEqual(minCount)
  return readMail(request, matches[0]!.ID)
}

/**
 * Every link into the app (APP_URL-prefixed) found in the message's text and HTML bodies,
 * de-duplicated, optionally narrowed to a path prefix such as `/verify-email`.
 */
export function extractLinks(
  message: Pick<MailpitMessage, 'Text' | 'HTML'>,
  pathPrefix = '/',
): URL[] {
  const escaped = APP_URL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const pattern = new RegExp(`${escaped}[^\\s"'<>)\\]]*`, 'g')
  const found = new Set<string>()
  for (const body of [message.Text ?? '', message.HTML ?? '']) {
    for (const match of body.matchAll(pattern)) {
      // HTML bodies escape `&` in query strings; the text body does not.
      found.add(match[0].replace(/&amp;/g, '&'))
    }
  }
  return [...found].map((href) => new URL(href)).filter((url) => url.pathname.startsWith(pathPrefix))
}

/** The first app link under `pathPrefix`, or a loud failure naming the mail it was missing from. */
export function extractLink(message: MailpitMessage, pathPrefix: string): URL {
  const [link] = extractLinks(message, pathPrefix)
  if (!link) {
    throw new Error(`no ${APP_URL}${pathPrefix}… link in mail "${message.Subject}":\n${message.Text}`)
  }
  return link
}
