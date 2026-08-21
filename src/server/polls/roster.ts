import { eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { formatOptionLabel } from '#/lib/time'

const BOM = '﻿'

/** RFC 4180 field quoting: quote whenever the field contains a comma, quote, or line break. */
function csvField(value: string): string {
  if (/[",\r\n]/.test(value)) {
    return `"${value.replace(/"/g, '""')}"`
  }
  return value
}

function csvRow(fields: string[]): string {
  return fields.map(csvField).join(',')
}

/**
 * Builds the owner-only roster export for a sign-up sheet: one row per claim (slot, capacity,
 * total claimed, participant name, email), and a single zero-claim row for a slot nobody has
 * taken yet (with empty participant/email). Prefixed with a UTF-8 BOM so spreadsheet apps that
 * sniff encoding (notably Excel) render non-ASCII names correctly.
 */
export async function buildRosterCsv(
  db: Db,
  pollId: string,
  opts: { locale: string },
): Promise<string> {
  const poll = await db.query.polls.findFirst({
    where: eq(polls.id, pollId),
    with: {
      options: { orderBy: (o, { asc }) => [asc(o.position)] },
      participants: { with: { votes: true } },
    },
  })
  if (!poll || poll.deletedAt) throw new AppError('NOT_FOUND')

  const rows: string[] = [csvRow(['slot', 'capacity', 'claimed', 'participant', 'email'])]

  for (const option of poll.options) {
    const label = formatOptionLabel(option, { locale: opts.locale, timeZone: poll.timezone })
    const slotLabel = [label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')
    const capacity = option.capacity === null ? '' : String(option.capacity)

    const claimants = poll.participants.filter((p) =>
      p.votes.some((v) => v.optionId === option.id && v.answer === 'yes'),
    )
    const claimed = String(claimants.length)

    if (claimants.length === 0) {
      rows.push(csvRow([slotLabel, capacity, claimed, '', '']))
    } else {
      for (const participant of claimants) {
        rows.push(csvRow([slotLabel, capacity, claimed, participant.name, participant.email ?? '']))
      }
    }
  }

  return BOM + rows.join('\r\n') + '\r\n'
}
