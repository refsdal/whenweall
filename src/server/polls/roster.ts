import { eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { polls } from '#/server/db/schema'
import { AppError } from '#/lib/errors'
import { formatOptionLabel } from '#/lib/time'

const BOM = '﻿'

/**
 * A field starting with `= + - @` (or a tab/CR, which some spreadsheet apps also treat as a
 * formula prefix once leading whitespace is trimmed) gets read as a formula by Excel/Sheets/
 * LibreOffice when the CSV is opened — a participant-supplied name like `=HYPERLINK(...)` would
 * otherwise execute in the poll owner's spreadsheet. Prefixing with a single quote defuses it; a
 * quote already forces text interpretation in every mainstream spreadsheet app and is invisible in
 * a normal cell.
 */
function escapeFormula(value: string): string {
  return /^[=+\-@\t\r]/.test(value) ? `'${value}` : value
}

/** RFC 4180 field quoting: quote whenever the field contains a comma, quote, or line break. */
function csvField(rawValue: string): string {
  const value = escapeFormula(rawValue)
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
