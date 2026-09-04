import { describe, expect, it } from 'vitest'
import {
  DIGEST_EVENTS,
  NOTIFICATION_EVENTS,
  SYSTEM_DEFAULTS,
  gridSchema,
  isDigestEvent,
  resolveChannels,
} from '#/lib/notifications'

describe('catalogue', () => {
  it('has every event covered by SYSTEM_DEFAULTS', () => {
    expect(NOTIFICATION_EVENTS).toHaveLength(11)
    for (const event of NOTIFICATION_EVENTS) {
      expect(SYSTEM_DEFAULTS[event]).toBeDefined()
    }
  })

  it('defaults withdrawals off and new responses on', () => {
    expect(SYSTEM_DEFAULTS['response.withdrawn'].email).toBe(false)
    expect(SYSTEM_DEFAULTS['response.created'].email).toBe(true)
  })

  it('treats activity events as digestible and lifecycle events as immediate', () => {
    expect(isDigestEvent('response.updated')).toBe(true)
    expect(isDigestEvent('comment.created')).toBe(true)
    expect(isDigestEvent('poll.closed')).toBe(false)
    expect(isDigestEvent('booking.created')).toBe(false)
    expect(DIGEST_EVENTS).toHaveLength(5)
  })
})

describe('resolveChannels', () => {
  it('falls back to system defaults when nothing is stored', () => {
    expect(resolveChannels('response.created', null, null)).toEqual(
      SYSTEM_DEFAULTS['response.created'],
    )
  })

  it('prefers user defaults over system defaults', () => {
    const defaults = { 'response.created': { email: false, push: false } }
    expect(resolveChannels('response.created', null, defaults)).toEqual({
      email: false,
      push: false,
    })
  })

  it('prefers a scope override over user defaults', () => {
    const defaults = { 'response.created': { email: false, push: false } }
    const override = { 'response.created': { email: true, push: false } }
    expect(resolveChannels('response.created', override, defaults)).toEqual({
      email: true,
      push: false,
    })
  })

  it('merges per key, so overriding one event leaves the others on their defaults', () => {
    const defaults = {
      'response.created': { email: false, push: false },
      'comment.created': { email: false, push: false },
    }
    const override = { 'response.created': { email: true, push: true } }
    expect(resolveChannels('comment.created', override, defaults)).toEqual({
      email: false,
      push: false,
    })
  })

  it('ignores unknown keys rather than throwing', () => {
    const parsed = gridSchema.parse({ 'not.an.event': { email: true, push: true } })
    expect(parsed).toEqual({})
  })

  it('keeps known keys through the schema', () => {
    const parsed = gridSchema.parse({
      'comment.created': { email: false, push: true },
      'not.an.event': { email: true, push: true },
    })
    expect(parsed).toEqual({ 'comment.created': { email: false, push: true } })
  })
})
