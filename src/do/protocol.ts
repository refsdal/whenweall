/** Wire protocol shared between the PollRoom durable object and the browser. Browser-safe: no server imports. */

import type { DigestEvent } from '#/lib/notifications'

export type PollEvent =
  | { type: 'poll.changed'; entity: 'poll' | 'participant' | 'vote' | 'comment' }
  | { type: 'presence'; count: number }

/**
 * One queued activity event awaiting the next digest. `actorUserId` is carried so the alarm can
 * suppress an item for the person who caused it — recipients are resolved at alarm time, not at
 * enqueue time, so a preference flipped during the debounce window still takes effect.
 */
export type DigestItem = {
  event: DigestEvent
  name: string
  at: string
  actorUserId: string | null
}
