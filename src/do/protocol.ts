/** Wire protocol shared between the PollRoom durable object and the browser. Browser-safe: no server imports. */

export type PollEvent =
  | { type: 'poll.changed'; entity: 'poll' | 'participant' | 'vote' | 'comment' }
  | { type: 'presence'; count: number }

export type DigestItem = { kind: 'vote' | 'comment'; name: string; at: string }
