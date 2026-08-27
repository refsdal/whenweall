import { env } from 'cloudflare:workers'
import { runInDurableObject } from 'cloudflare:test'
import { describe, expect, it, vi } from 'vitest'
import { newId } from '#/lib/ids'

function stub() {
  // A fresh name per test, so counters from one test cannot leak into the next.
  return env.RATE_LIMIT_ROOM.getByName(`test-${newId()}`)
}

describe('RateLimitRoom', () => {
  it('allows up to max within a window, then refuses', async () => {
    const room = stub()
    const results = []
    for (let i = 0; i < 4; i += 1) results.push(await room.consume('k', 10, 3))

    expect(results.map((r) => r.allowed)).toEqual([true, true, true, false])
  })

  it('reports retryAfter in whole seconds, never zero', async () => {
    const room = stub()
    await room.consume('k', 10, 1)
    const denied = await room.consume('k', 10, 1)

    expect(denied.allowed).toBe(false)
    expect(denied.retryAfter).toBeGreaterThan(0)
    expect(denied.retryAfter).toBeLessThanOrEqual(10)
    expect(Number.isInteger(denied.retryAfter)).toBe(true)
  })

  it('starts a fresh window once the old one has closed', async () => {
    vi.useFakeTimers()
    try {
      const room = stub()
      await room.consume('k', 10, 1)
      expect((await room.consume('k', 10, 1)).allowed).toBe(false)

      vi.setSystemTime(Date.now() + 10_001)
      expect((await room.consume('k', 10, 1)).allowed).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })

  it('counts each key separately', async () => {
    const room = stub()
    await room.consume('a', 10, 1)

    expect((await room.consume('a', 10, 1)).allowed).toBe(false)
    expect((await room.consume('b', 10, 1)).allowed).toBe(true)
  })

  // The property the default in-isolate `Map` backend fails, and the whole reason this durable
  // object exists: two requests that land on different isolates must share one counter.
  it('shares a counter across separate stubs for the same name', async () => {
    const name = `shared-${newId()}`

    expect((await env.RATE_LIMIT_ROOM.getByName(name).consume('k', 10, 1)).allowed).toBe(true)
    expect((await env.RATE_LIMIT_ROOM.getByName(name).consume('k', 10, 1)).allowed).toBe(false)
  })

  it('keeps nothing in storage, so an eviction costs at most one window', async () => {
    const room = stub()
    await room.consume('k', 10, 3)

    const stored = await runInDurableObject(room, (_instance, state) => state.storage.list())
    expect(stored.size).toBe(0)
  })

  it('sweeps closed windows instead of growing without bound', async () => {
    vi.useFakeTimers()
    try {
      const room = stub()
      for (let i = 0; i < 300; i += 1) await room.consume(`k${i}`, 1, 1)
      expect(await room.size()).toBeGreaterThan(0)

      // Every window above has closed; the next write should collect them.
      vi.setSystemTime(Date.now() + 2_000)
      await room.consume('fresh', 10, 1)

      expect(await room.size()).toBeLessThan(300)
    } finally {
      vi.useRealTimers()
    }
  })
})
