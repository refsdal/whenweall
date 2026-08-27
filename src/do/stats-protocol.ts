/** Wire protocol shared between the StatsRoom durable object and the browser. Browser-safe: no server imports. */

/**
 * Global, anonymous usage totals. Every field is a monotonic lifetime count of *submissions*, not
 * a snapshot of current state — someone who answers "yes" and later changes it to "no" adds one to
 * each. See the spec's §1: a counter that ticks downwards while a visitor watches reads as a bug,
 * and the copy says "responses recorded" rather than "people said yes" for exactly this reason.
 */
export type UsageStats = {
  pollsCreated: number
  responsesYes: number
  responsesIfNeedBe: number
  responsesNo: number
}

export const EMPTY_STATS: UsageStats = {
  pollsCreated: 0,
  responsesYes: 0,
  responsesIfNeedBe: 0,
  responsesNo: 0,
}

export type StatsEvent = { type: 'stats'; stats: UsageStats }

/** Total responses across all three answers — the headline number the landing section leads with. */
export function totalResponses(stats: UsageStats): number {
  return stats.responsesYes + stats.responsesIfNeedBe + stats.responsesNo
}
