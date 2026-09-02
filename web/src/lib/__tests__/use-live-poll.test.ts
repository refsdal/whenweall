import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { type PollEvent, useLivePoll } from '#/lib/use-live-poll'

const POLL_ID = 'abcdefghijkl'

/** Mock WS server standing in for `internal/rooms`'s hub — see `room-socket.test.ts` for the
 * protocol-level coverage of `connectRoom` itself; this file only checks `useLivePoll`'s own
 * wiring on top of it (presence tracking, the synthetic `poll.changed` a snapshot/resync forward,
 * the guest token query param). */
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

beforeEach(() => {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('useLivePoll', () => {
  it('opens a websocket to the poll room on the current host', () => {
    renderHook(() => useLivePoll(POLL_ID, vi.fn()))

    expect(FakeSocket.instances).toHaveLength(1)
    expect(FakeSocket.last.url).toBe(`ws://${window.location.host}/api/v1/polls/${POLL_ID}/ws`)
  })

  it('appends the guest edit token as ?token=', () => {
    renderHook(() => useLivePoll(POLL_ID, vi.fn(), 'my-token'))

    expect(FakeSocket.last.url).toContain('token=my-token')
  })

  it('reports connected once the snapshot frame arrives', () => {
    const { result } = renderHook(() => useLivePoll(POLL_ID, vi.fn()))
    expect(result.current.connected).toBe(false)

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 1, data: null })
    })

    expect(result.current.connected).toBe(true)
  })

  it('forwards a poll.changed event with its entity', () => {
    const onEvent = vi.fn()
    renderHook(() => useLivePoll(POLL_ID, onEvent))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 1, data: null })
    })
    onEvent.mockClear() // drop the synthetic poll.changed the snapshot itself forwards

    act(() => FakeSocket.last.send({ type: 'poll.changed', seq: 2, entity: 'vote' }))

    expect(onEvent).toHaveBeenCalledWith({ type: 'poll.changed', entity: 'vote' })
  })

  it('forwards a synthetic poll.changed for the snapshot every connect gets', () => {
    const onEvent = vi.fn()
    renderHook(() => useLivePoll(POLL_ID, onEvent))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 1, data: null })
    })

    expect(onEvent).toHaveBeenCalledWith({ type: 'poll.changed', entity: 'poll' })
  })

  it('forwards a synthetic poll.changed on resync', () => {
    const onEvent = vi.fn()
    renderHook(() => useLivePoll(POLL_ID, onEvent))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 1, data: null })
    })
    onEvent.mockClear()

    act(() => FakeSocket.last.send({ type: 'resync' }))

    expect(onEvent).toHaveBeenCalledWith({ type: 'poll.changed', entity: 'poll' })
  })

  it('tracks presence from presence events', () => {
    const { result } = renderHook(() => useLivePoll(POLL_ID, vi.fn()))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'presence', seq: 1, count: 3 })
    })

    expect(result.current.presence).toBe(3)
  })

  it('does not reopen the socket when only the callback identity changes', () => {
    const { rerender } = renderHook(
      ({ cb }: { cb: (e: PollEvent) => void }) => useLivePoll(POLL_ID, cb),
      { initialProps: { cb: vi.fn() } },
    )

    rerender({ cb: vi.fn() })

    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('calls the latest callback after a rerender', () => {
    const second = vi.fn()
    const { rerender } = renderHook(
      ({ cb }: { cb: (e: PollEvent) => void }) => useLivePoll(POLL_ID, cb),
      { initialProps: { cb: vi.fn() as (e: PollEvent) => void } },
    )
    rerender({ cb: second })

    act(() => FakeSocket.last.send({ type: 'poll.changed', seq: 5, entity: 'comment' }))

    expect(second).toHaveBeenCalledWith({ type: 'poll.changed', entity: 'comment' })
  })

  it('reconnects with a backoff after the socket drops', () => {
    vi.useFakeTimers()
    renderHook(() => useLivePoll(POLL_ID, vi.fn()))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.onclose?.()
    })
    expect(FakeSocket.instances).toHaveLength(1)

    act(() => vi.advanceTimersByTime(1000))
    expect(FakeSocket.instances).toHaveLength(2)

    // Second failure without a successful open backs off further than one second.
    act(() => FakeSocket.last.onclose?.())
    act(() => vi.advanceTimersByTime(1000))
    expect(FakeSocket.instances).toHaveLength(2)

    act(() => vi.advanceTimersByTime(1000))
    expect(FakeSocket.instances).toHaveLength(3)
  })

  it('closes the socket and stops reconnecting on unmount', () => {
    vi.useFakeTimers()
    const { unmount } = renderHook(() => useLivePoll(POLL_ID, vi.fn()))
    const socket = FakeSocket.last

    act(() => socket.open())
    unmount()

    expect(socket.close).toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(60_000))
    expect(FakeSocket.instances).toHaveLength(1)
  })
})
