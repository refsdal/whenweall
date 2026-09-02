import type { PollView } from '#/api/types'
import type { AppLocale } from '#/app.config'

/**
 * Who is looking at the poll, and in whose terms it should be rendered.
 *
 * `participantId` is "your row" — resolved from the session (a signed-in participant) or from the
 * edit token this browser stored when a guest voted. It is only known on the client, so it starts
 * `null` during SSR and fills in after mount.
 */
export type ViewerState = {
  userId: string | null
  participantId: string | null
  editToken: string | null
  isOwner: boolean
  locale: AppLocale
  timeZone: string
}

/** Voting is possible only while the poll is open — closed and finalized polls are read-only. */
export function canVote(poll: PollView): boolean {
  return poll.status === 'open'
}

/** The owner can edit anyone; a participant can edit their own row. */
export function canEditParticipant(
  poll: PollView,
  viewer: ViewerState,
  participantId: string,
): boolean {
  if (!canVote(poll)) return false
  return viewer.isOwner || viewer.participantId === participantId
}
