import { describe, expect, it } from 'vitest'
import * as pollsFunctions from '#/server/polls/polls.functions'
import * as participantsFunctions from '#/server/polls/participants.functions'

// Server functions pull in `cloudflare:workers`, better-auth, and rate-limit/turnstile modules
// that only resolve correctly inside the Workers runtime. This proves the module graph for both
// function files loads and evaluates cleanly in workerd (no top-level throw, no unresolved
// import) — a lightweight substitute for exercising them over the TanStack Start RPC transport,
// which is awkward to hand-construct outside a real client fetcher.
describe('polls.functions module graph', () => {
  it.each([
    'getPoll',
    'createPoll',
    'updatePoll',
    'setPollStatus',
    'finalizePoll',
    'deletePoll',
    'duplicatePoll',
    'listMyPolls',
    'updateNotificationPrefs',
  ] as const)('exports a callable %s server function', (name) => {
    expect(typeof pollsFunctions[name]).toBe('function')
  })
})

describe('participants.functions module graph', () => {
  it.each([
    'addParticipant',
    'updateParticipant',
    'removeParticipant',
    'addComment',
    'deleteComment',
  ] as const)('exports a callable %s server function', (name) => {
    expect(typeof participantsFunctions[name]).toBe('function')
  })
})
