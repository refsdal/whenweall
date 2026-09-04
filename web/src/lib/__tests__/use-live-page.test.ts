import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useLivePage } from '#/lib/use-live-page'

const PAGE_ID = 'page-abcdef'

/** Mock WS server standing in for `internal/rooms`'s hub — the same shape `use-live-poll.test.ts`
 * uses; this file only checks `useLivePage`'s own wiring (synthetic `page.changed` on snapshot
 * and resync, no `?since=` on reconnect). */
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

describe('useLivePage', () => {
  it('opens a websocket to the booking-page room', () => {
    renderHook(() => useLivePage(PAGE_ID, vi.fn()))

    expect(FakeSocket.last.url).toBe(
      `ws://${window.location.host}/api/v1/booking-pages/${PAGE_ID}/ws`,
    )
  })

  it('forwards a synthetic page.changed for the snapshot and for resync', () => {
    const onEvent = vi.fn()
    renderHook(() => useLivePage(PAGE_ID, onEvent))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 1, data: null })
    })
    expect(onEvent).toHaveBeenCalledTimes(1)
    expect(onEvent).toHaveBeenCalledWith({ type: 'page.changed' })

    act(() => FakeSocket.last.send({ type: 'resync' }))
    expect(onEvent).toHaveBeenCalledTimes(2)
  })

  it('reconnects without ?since= (the snapshot is ground truth)', () => {
    vi.useFakeTimers()
    renderHook(() => useLivePage(PAGE_ID, vi.fn()))

    act(() => {
      FakeSocket.last.open()
      FakeSocket.last.send({ type: 'snapshot', seq: 4, data: null })
      FakeSocket.last.send({ type: 'page.changed', seq: 6 })
      FakeSocket.last.onclose?.()
    })
    act(() => vi.advanceTimersByTime(1000))

    expect(FakeSocket.instances).toHaveLength(2)
    expect(FakeSocket.last.url).not.toContain('since=')
  })
})
