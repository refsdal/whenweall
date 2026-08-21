import { env } from 'cloudflare:workers'
import type { DigestItem, PollEvent } from '#/do/protocol'
import type { PollRoom } from '#/do/PollRoom'

/**
 * Thin client for talking to a poll's `PollRoom` durable object from server functions and the
 * `PollRoom` alarm's callers. Every notification helper below is best-effort: a stalled/evicted DO
 * must never fail the request that triggered the notification, so failures are caught and logged.
 */
export function pollRoom(pollId: string): DurableObjectStub<PollRoom> {
  return env.POLL_ROOM.getByName(pollId)
}

export async function notifyChanged(
  pollId: string,
  entity: Extract<PollEvent, { type: 'poll.changed' }>['entity'],
): Promise<void> {
  try {
    await pollRoom(pollId).broadcast(pollId, { type: 'poll.changed', entity })
  } catch (err) {
    console.error('[do-client] notifyChanged failed', err)
  }
}

export async function queueDigest(pollId: string, item: DigestItem): Promise<void> {
  try {
    await pollRoom(pollId).enqueueDigest(pollId, item)
  } catch (err) {
    console.error('[do-client] queueDigest failed', err)
  }
}

export async function syncDeadline(pollId: string, deadlineAt: string | null): Promise<void> {
  try {
    await pollRoom(pollId).syncDeadline(pollId, deadlineAt)
  } catch (err) {
    console.error('[do-client] syncDeadline failed', err)
  }
}
