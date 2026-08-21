import { eq } from 'drizzle-orm'
import { env } from 'cloudflare:workers'
import type { Db } from '#/server/db/client'
import { pollOptions, polls, type OptionKind } from '#/server/db/schema'
import { buildIcs } from '#/lib/ics'

type IcsStart = Parameters<typeof buildIcs>[0]['start']

/**
 * Maps a poll option row to a `buildIcs` `start`. Returns null for a plain-text option — it has no
 * calendar meaning. Shared by the calendar.ics route and the finalized-poll notification email so
 * the two can't drift on what counts as "has a date".
 */
export function icsStartFromOption(option: {
  kind: OptionKind
  startAt: string | null
  endAt: string | null
}): IcsStart | null {
  if (option.kind === 'text') return null
  if (option.kind === 'date') return { date: option.startAt! }
  return { dateTime: option.startAt!, endDateTime: option.endAt }
}

/**
 * Builds an .ics file for a poll's finalized option. Returns null when the poll isn't finalized,
 * is missing/deleted, or the finalized option is a plain-text option (no calendar meaning).
 */
export async function buildPollIcs(db: Db, pollId: string): Promise<string | null> {
  const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
  if (!poll || poll.deletedAt) return null
  if (poll.status !== 'finalized' || !poll.finalizedOptionId) return null

  const option = await db.query.pollOptions.findFirst({
    where: eq(pollOptions.id, poll.finalizedOptionId),
  })
  if (!option) return null

  const start = icsStartFromOption(option)
  if (!start) return null

  return buildIcs({
    uid: `${poll.id}@samla`,
    title: poll.title,
    description: poll.description,
    location: poll.location,
    url: `${env.APP_URL}/p/${poll.id}`,
    start,
  })
}
