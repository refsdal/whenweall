import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useLivePoll } from '#/lib/use-live-poll'
import type { PollEvent } from '#/do/protocol'

const POLL_ID = 'abcdefghijkl'

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

  emit(event: PollEvent) {
    this.onmessage?.({ data: JSON.stringify(event) })
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
    expect(FakeSocket.last.url).toBe(`ws://${window.location.host}/api/polls/${POLL_ID}/ws`)
  })

  it('reports the connection as open once the socket opens', () => {
    const { result } = renderHook(() => useLivePoll(POLL_ID, vi.fn()))
    expect(result.current.connected).toBe(false)

    act(() => FakeSocket.last.open())

    expect(result.current.connected).toBe(true)
  })

  it('forwards parsed events to onEvent', () => {
    const onEvent = vi.fn()
    renderHook(() => useLivePoll(POLL_ID, onEvent))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.emit({ type: 'poll.changed', entity: 'vote' })
    })

    expect(onEvent).toHaveBeenCalledWith({ type: 'poll.changed', entity: 'vote' })
  })

  it('ignores unparseable frames', () => {
    const onEvent = vi.fn()
    renderHook(() => useLivePoll(POLL_ID, onEvent))

    act(() => {
      FakeSocket.last.onmessage?.({ data: 'pong' })
    })

    expect(onEvent).not.toHaveBeenCalled()
  })

  it('tracks presence from presence events', () => {
    const { result } = renderHook(() => useLivePoll(POLL_ID, vi.fn()))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.emit({ type: 'presence', count: 3 })
    })

    expect(result.current.presence).toBe(3)
  })

  it('does not reopen the socket when only the callback identity changes', () => {
    const { rerender } = renderHook(
      ({ cb }: { cb: (e: PollEvent) => void }) => useLivePoll(POLL_ID, cb),
      {
        initialProps: { cb: vi.fn() },
      },
    )

    rerender({ cb: vi.fn() })

    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('calls the latest callback after a rerender', () => {
    const second = vi.fn()
    const { rerender } = renderHook(
      ({ cb }: { cb: (e: PollEvent) => void }) => useLivePoll(POLL_ID, cb),
      {
        initialProps: { cb: vi.fn() as (e: PollEvent) => void },
      },
    )
    rerender({ cb: second })

    act(() => FakeSocket.last.emit({ type: 'poll.changed', entity: 'comment' }))

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
