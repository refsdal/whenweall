import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { connectRoom } from '#/lib/room-socket'

/**
 * A mock WS server standing in for `internal/rooms`'s hub: `send` pushes a frame exactly the
 * shape `internal/rooms/PROTOCOL.md` describes (nested snapshot, flattened everything else), and
 * `FakeSocket.last.url` lets a test inspect the query string `connectRoom` built for a given
 * (re)connect.
 */
class FakeSocket {
  static instances: FakeSocket[] = []

  url: string
  readyState = 0
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  close = vi.fn(() => {
    this.readyState = 3
    this.onclose?.()
  })

  constructor(url: string) {
    this.url = url
    FakeSocket.instances.push(this)
  }

  open() {
    this.readyState = 1
    this.onopen?.()
  }

  send(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }

  static get last(): FakeSocket {
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('no socket was opened')
    return socket
  }
}

const PATH = '/api/v1/polls/abc/ws'

beforeEach(() => {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('connectRoom', () => {
  it('delivers the snapshot (unwrapped from its nested data) before any live event', () => {
    const onSnapshot = vi.fn()
    const onEvent = vi.fn()
    connectRoom({ path: PATH, onSnapshot, onEvent, onResync: vi.fn() })

    FakeSocket.last.open()
    FakeSocket.last.send({ type: 'snapshot', seq: 5, data: { title: 'hi' } })
    FakeSocket.last.send({ type: 'poll.changed', seq: 6, entity: 'vote' })

    expect(onSnapshot).toHaveBeenCalledWith({ title: 'hi' }, 5)
    expect(onEvent).toHaveBeenCalledWith('poll.changed', { entity: 'vote' }, 6)
    expect(onSnapshot.mock.invocationCallOrder[0]).toBeLessThan(onEvent.mock.invocationCallOrder[0])
  })

  it('never puts anything but the path on the URL on a first connect', () => {
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })

    expect(FakeSocket.last.url).toBe(`ws://${window.location.host}${PATH}`)
  })

  it('omits ?since= on reconnect when backfill is false (snapshot-as-ground-truth consumers)', () => {
    vi.useFakeTimers()
    connectRoom({ path: PATH, backfill: false, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })
    FakeSocket.last.open()
    FakeSocket.last.send({ type: 'snapshot', seq: 5, data: null })
    FakeSocket.last.send({ type: 'poll.changed', seq: 12, entity: 'vote' })
    FakeSocket.last.onclose?.()

    vi.advanceTimersByTime(1000)

    expect(FakeSocket.instances).toHaveLength(2)
    expect(FakeSocket.last.url).not.toContain('since=')
  })

  it('flattens a live frame into (type, data-without-type-or-seq, seq)', () => {
    const onEvent = vi.fn()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent, onResync: vi.fn() })
    FakeSocket.last.open()

    FakeSocket.last.send({ type: 'presence', seq: 3, count: 2 })

    expect(onEvent).toHaveBeenCalledWith('presence', { count: 2 }, 3)
  })

  it('dedupes by seq SET membership: a redelivered duplicate frame is dropped', () => {
    const onEvent = vi.fn()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent, onResync: vi.fn() })
    FakeSocket.last.open()

    FakeSocket.last.send({ type: 'poll.changed', seq: 9, entity: 'vote' })
    FakeSocket.last.send({ type: 'poll.changed', seq: 9, entity: 'vote' }) // at-least-once redelivery

    expect(onEvent).toHaveBeenCalledTimes(1)
  })

  it('never filters by seq <= lastSeen — a late committer with a lower seq still fires', () => {
    const onEvent = vi.fn()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent, onResync: vi.fn() })
    FakeSocket.last.open()

    FakeSocket.last.send({ type: 'poll.changed', seq: 12, entity: 'vote' })
    FakeSocket.last.send({ type: 'poll.changed', seq: 8, entity: 'comment' }) // committed after 12, id lower

    expect(onEvent).toHaveBeenCalledTimes(2)
    expect(onEvent).toHaveBeenNthCalledWith(2, 'poll.changed', { entity: 'comment' }, 8)
  })

  it('reconnects with ?since=<max seq observed>, not the last-arrived seq', () => {
    vi.useFakeTimers()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })
    FakeSocket.last.open()

    FakeSocket.last.send({ type: 'snapshot', seq: 5, data: null })
    FakeSocket.last.send({ type: 'poll.changed', seq: 12, entity: 'vote' })
    FakeSocket.last.send({ type: 'poll.changed', seq: 8, entity: 'comment' }) // arrives after 12, lower seq
    FakeSocket.last.onclose?.()

    vi.advanceTimersByTime(1000)

    expect(FakeSocket.instances).toHaveLength(2)
    expect(FakeSocket.last.url).toContain('since=12')
  })

  it('backs off 1s -> 2s -> ... capped at 30s between reconnect attempts', () => {
    vi.useFakeTimers()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })

    FakeSocket.last.open()
    FakeSocket.last.onclose?.()
    expect(FakeSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(999)
    expect(FakeSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(FakeSocket.instances).toHaveLength(2)

    FakeSocket.last.onclose?.()
    vi.advanceTimersByTime(1999)
    expect(FakeSocket.instances).toHaveLength(2)
    vi.advanceTimersByTime(1)
    expect(FakeSocket.instances).toHaveLength(3)
  })

  it('ignores pong frames', () => {
    const onEvent = vi.fn()
    const onSnapshot = vi.fn()
    connectRoom({ path: PATH, onSnapshot, onEvent, onResync: vi.fn() })
    FakeSocket.last.open()

    FakeSocket.last.send({ type: 'pong' })

    expect(onEvent).not.toHaveBeenCalled()
    expect(onSnapshot).not.toHaveBeenCalled()
  })

  it('ignores unparseable frames', () => {
    const onEvent = vi.fn()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent, onResync: vi.fn() })
    FakeSocket.last.open()

    FakeSocket.last.onmessage?.({ data: 'not json' })

    expect(onEvent).not.toHaveBeenCalled()
  })

  it('calls onResync for a resync frame (which carries no seq) and drops the ?since= cursor', () => {
    vi.useFakeTimers()
    const onResync = vi.fn()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync })
    FakeSocket.last.open()

    FakeSocket.last.send({ type: 'snapshot', seq: 5, data: null })
    FakeSocket.last.send({ type: 'poll.changed', seq: 12, entity: 'vote' })
    FakeSocket.last.send({ type: 'resync' })

    expect(onResync).toHaveBeenCalledTimes(1)

    // The resync itself doesn't close the connection (it fires over the still-open socket) — a
    // later, genuine disconnect must not fall back to the pre-resync cursor.
    FakeSocket.last.onclose?.()
    vi.advanceTimersByTime(1000)

    expect(FakeSocket.last.url).not.toContain('since=')
  })

  it('a snapshot after a resync re-establishes the cursor for the next reconnect', () => {
    vi.useFakeTimers()
    connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })
    FakeSocket.last.open()
    FakeSocket.last.send({ type: 'poll.changed', seq: 12, entity: 'vote' })
    FakeSocket.last.send({ type: 'resync' })
    FakeSocket.last.onclose?.()
    vi.advanceTimersByTime(1000)

    expect(FakeSocket.last.url).not.toContain('since=')

    FakeSocket.last.open()
    FakeSocket.last.send({ type: 'snapshot', seq: 20, data: null })
    FakeSocket.last.onclose?.()
    vi.advanceTimersByTime(1000)

    expect(FakeSocket.last.url).toContain('since=20')
  })

  it('close() tears down the socket and stops reconnecting', () => {
    vi.useFakeTimers()
    const room = connectRoom({ path: PATH, onSnapshot: vi.fn(), onEvent: vi.fn(), onResync: vi.fn() })
    FakeSocket.last.open()

    room.close()

    expect(FakeSocket.last.close).toHaveBeenCalled()
    vi.advanceTimersByTime(60_000)
    expect(FakeSocket.instances).toHaveLength(1)
  })
})
