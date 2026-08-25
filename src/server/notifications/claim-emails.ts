import { and, eq, inArray } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { participants, pollOptions, polls, votes } from '#/server/db/schema'
import type { IcsEvent } from '#/lib/ics'
import { buildIcsMulti } from '#/lib/ics'
import { formatOptionLabel } from '#/lib/time'
import { sendMail } from '#/server/mailer/mailer'
import { renderClaimConfirmation } from '#/server/mailer/templates'
import { icsStartFromOption } from '#/server/polls/ics'

type ClaimEmailEnv = {
  EMAIL?: SendEmail
  EMAIL_FROM: string
  APP_URL: string
  APP_ENV?: string
}

/**
 * Sends (or re-sends) the claim confirmation email for one participant's current claims on a
 * sign-up sheet — called on first claim and again whenever their claims change. Always
 * best-effort: never throws, and returns `false` for every "nothing to send" case (poll/
 * participant missing, no email on file, no claims left) as well as for a genuine send failure,
 * so the caller doesn't need to distinguish "skipped" from "failed" to decide whether to retry.
 */
export async function sendClaimConfirmation(
  env: ClaimEmailEnv,
  input: { db: Db; pollId: string; participantId: string },
  deps: { mailer?: typeof sendMail } = {},
): Promise<boolean> {
  const mailer = deps.mailer ?? sendMail
  const { db, pollId, participantId } = input

  try {
    const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
    if (!poll || poll.deletedAt) return false

    const participant = await db.query.participants.findFirst({
      where: eq(participants.id, participantId),
    })
    if (!participant || participant.pollId !== pollId || !participant.email) return false

    const claimedVotes = await db.query.votes.findMany({
      where: and(eq(votes.participantId, participantId), eq(votes.answer, 'yes')),
    })
    if (claimedVotes.length === 0) return false

    const options = await db.query.pollOptions.findMany({
      where: inArray(
        pollOptions.id,
        claimedVotes.map((v) => v.optionId),
      ),
    })
    options.sort((a, b) => a.position - b.position)

    const locale = participant.locale ?? 'en'
    const pollUrl = `${env.APP_URL}/p/${poll.id}`

    const slots = options.map((option) => {
      const label = formatOptionLabel(option, { locale, timeZone: poll.timezone })
      return [label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')
    })

    const icsEvents: IcsEvent[] = []
    for (const option of options) {
      const start = icsStartFromOption(option)
      if (!start) continue
      icsEvents.push({
        uid: `${poll.id}-${option.id}@whenweall`,
        title: poll.title,
        description: poll.description,
        location: poll.location,
        url: pollUrl,
        start,
      })
    }
    const ics = icsEvents.length > 0 ? buildIcsMulti(icsEvents) : null

    const rendered = await renderClaimConfirmation({
      name: participant.name,
      pollTitle: poll.title,
      pollUrl,
      slots,
      locale,
    })

    return await mailer(env, {
      to: participant.email,
      ...rendered,
      attachments: ics
        ? [{ filename: 'calendar.ics', content: ics, type: 'text/calendar' }]
        : undefined,
    })
  } catch (err) {
    console.error('[claim-emails] failed to send', err)
    return false
  }
}
