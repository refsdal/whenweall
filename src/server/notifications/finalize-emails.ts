import { buildIcs } from '#/lib/ics'
import { formatOptionLabel } from '#/lib/time'
import { sendMail } from '#/server/mailer/mailer'
import { renderFinalized } from '#/server/mailer/templates'
import { icsStartFromOption } from '#/server/polls/ics'
import type { finalizePoll } from '#/server/polls/service'

type FinalizeResult = Awaited<ReturnType<typeof finalizePoll>>

type FinalizeEmailsEnv = {
  EMAIL?: SendEmail
  EMAIL_FROM: string
  APP_URL: string
  APP_ENV?: string
}

function buildOptionIcs(env: FinalizeEmailsEnv, r: FinalizeResult): string | null {
  const { poll, option } = r
  const start = icsStartFromOption(option)
  if (!start) return null

  return buildIcs({
    uid: `${poll.id}@whenweall`,
    title: poll.title,
    description: poll.description,
    location: poll.location,
    url: `${env.APP_URL}/p/${poll.id}`,
    start,
  })
}

/**
 * Sends the "poll finalized" notification to every recipient in `r.recipients` (already deduped by
 * email in `finalizePoll`), attaching a `calendar.ics` when the finalized option has a calendar
 * meaning. Never throws — a failed send is counted in `failed` and the rest still go out.
 */
export async function sendFinalizedEmails(
  env: FinalizeEmailsEnv,
  r: FinalizeResult,
  deps: { mailer?: typeof sendMail } = {},
): Promise<{ sent: number; failed: number }> {
  const mailer = deps.mailer ?? sendMail
  const { poll, option, recipients } = r
  const ics = buildOptionIcs(env, r)

  let sent = 0
  let failed = 0

  for (const recipient of recipients) {
    try {
      const locale = recipient.locale ?? 'en'
      const label = formatOptionLabel(option, { locale, timeZone: poll.timezone })
      const optionLabel = [label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')

      const rendered = await renderFinalized({
        pollTitle: poll.title,
        pollUrl: `${env.APP_URL}/p/${poll.id}`,
        optionLabel,
        recipientName: recipient.name,
        locale,
      })

      const ok = await mailer(env, {
        to: recipient.email,
        ...rendered,
        attachments: ics
          ? [{ filename: 'calendar.ics', content: ics, type: 'text/calendar' }]
          : undefined,
      })

      if (ok) sent += 1
      else failed += 1
    } catch (err) {
      console.error('[finalize-emails] failed to send', err)
      failed += 1
    }
  }

  return { sent, failed }
}
