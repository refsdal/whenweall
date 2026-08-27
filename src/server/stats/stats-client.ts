import { env } from 'cloudflare:workers'
import type { StatsRoom } from '#/do/StatsRoom'
import { EMPTY_STATS, type UsageStats } from '#/do/stats-protocol'
import type { Answer } from '#/lib/scoring'

/** The one instance. Global counters have no natural partition key — see the spec's §2 for the
 * contention limit this accepts and the documented point at which to revisit it. */
export const STATS_ROOM_NAME = 'global'

export function statsRoom(): DurableObjectStub<StatsRoom> {
  return env.STATS_ROOM.getByName(STATS_ROOM_NAME)
}

/**
 * Every helper below is best-effort: a marketing counter must never fail the request that
 * triggered it, so failures are caught and logged. Same contract as
 * `notifications/do-client.ts`.
 */
export async function recordPollCreated(): Promise<void> {
  try {
    await statsRoom().recordPollCreated()
  } catch (err) {
    console.error('[stats-client] recordPollCreated failed', err)
  }
}

/** The outcome counter — a poll whose organiser picked a winning time. */
export async function recordPollFinalized(): Promise<void> {
  try {
    await statsRoom().recordPollFinalized()
  } catch (err) {
    console.error('[stats-client] recordPollFinalized failed', err)
  }
}

export async function recordResponses(answers: Answer[]): Promise<void> {
  if (answers.length === 0) return
  try {
    await statsRoom().recordResponses(answers)
  } catch (err) {
    console.error('[stats-client] recordResponses failed', err)
  }
}

/**
 * Read for the landing page's server render. Falls back to zeroes rather than throwing — the
 * landing page must render even if the durable object is unreachable.
 */
export async function readUsageStats(): Promise<UsageStats> {
  try {
    return await statsRoom().read()
  } catch (err) {
    console.error('[stats-client] readUsageStats failed', err)
    return { ...EMPTY_STATS }
  }
}
