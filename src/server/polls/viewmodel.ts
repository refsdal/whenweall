import type { OptionKind, PollStatus, PollType } from '#/server/db/schema'
import type { Answer, OptionScore } from '#/lib/scoring'

export type PollOptionView = {
  id: string
  position: number
  kind: OptionKind
  startAt: string | null
  endAt: string | null
  label: string | null
  capacity: number | null
}

export type ParticipantView = {
  id: string
  name: string
  userId: string | null
  hasEmail: boolean
  votes: Record<string, Answer>
  createdAt: string
}

export type CommentView = {
  id: string
  authorName: string
  body: string
  createdAt: string
  userId: string | null
  participantId: string | null
}

export type PollView = {
  id: string
  type: PollType
  title: string
  description: string | null
  location: string | null
  timezone: string
  status: PollStatus
  deadlineAt: string | null
  finalizedOptionId: string | null
  createdAt: string
  settings: {
    requireParticipantEmail: boolean
    allowComments: boolean
    allowIfNeedBe: boolean
    signupMaxClaims: number
  }
  notifications: { notifyOnVote: boolean; notifyOnComment: boolean } | null
  owner: { id: string; name: string }
  isOwner: boolean
  options: PollOptionView[]
  participants: ParticipantView[]
  comments: CommentView[]
  scores: Record<string, OptionScore>
  bestOptionId: string | null
  claims: Record<string, { count: number; capacity: number | null; full: boolean }>
}

export type PollSummary = {
  id: string
  title: string
  type: PollType
  status: PollStatus
  deadlineAt: string | null
  participantCount: number
  createdAt: string
  updatedAt: string
}
