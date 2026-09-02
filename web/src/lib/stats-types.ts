/**
 * Local replacement for the old `#/do/stats-protocol` (a Cloudflare Durable Object wire type that
 * no longer exists): the field vocabulary ported field-for-field into
 * `internal/rooms/stats.go`'s `UsageStats` struct, so this type just mirrors that struct's own
 * `json` tags directly rather than re-deriving them from a server import.
 */
export type UsageStats = {
  /** Polls whose organiser picked a winning time. The outcome number: it says the product worked,
   * not merely that someone tried it. Sign-up sheets cannot be finalized and never count here. */
  pollsFinalized: number
  pollsCreated: number
  responsesYes: number
  responsesIfNeedBe: number
  responsesNo: number
}

export const EMPTY_STATS: UsageStats = {
  pollsFinalized: 0,
  pollsCreated: 0,
  responsesYes: 0,
  responsesIfNeedBe: 0,
  responsesNo: 0,
}

/** Total responses across all three answers — the headline number the landing section leads with. */
export function totalResponses(stats: UsageStats): number {
  return stats.responsesYes + stats.responsesIfNeedBe + stats.responsesNo
}
