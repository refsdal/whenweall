import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useNewlyArrived } from '#/lib/use-newly-arrived'

describe('useNewlyArrived', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('reports nothing for the list a visitor lands on', () => {
    const { result } = renderHook(() => useNewlyArrived(['a', 'b']))
    expect([...result.current]).toEqual([])
  })

  it('reports ids that were not in the previous list', () => {
    const { result, rerender } = renderHook(({ ids }) => useNewlyArrived(ids), {
      initialProps: { ids: ['a', 'b'] },
    })

    rerender({ ids: ['a', 'b', 'c'] })
    expect([...result.current]).toEqual(['c'])
  })

  it('forgets them once the highlight has run', () => {
    const { result, rerender } = renderHook(({ ids }) => useNewlyArrived(ids, 1000), {
      initialProps: { ids: ['a'] },
    })

    rerender({ ids: ['a', 'b'] })
    expect([...result.current]).toEqual(['b'])

    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect([...result.current]).toEqual([])
  })

  it('does not re-report an id when an unrelated row is removed', () => {
    const { result, rerender } = renderHook(({ ids }) => useNewlyArrived(ids, 1000), {
      initialProps: { ids: ['a', 'b'] },
    })

    rerender({ ids: ['a', 'b', 'c'] })
    act(() => {
      vi.advanceTimersByTime(1000)
    })

    rerender({ ids: ['b', 'c'] })
    expect([...result.current]).toEqual([])
  })

  it('reports every id in a batch that arrives together', () => {
    const { result, rerender } = renderHook(({ ids }) => useNewlyArrived(ids), {
      initialProps: { ids: ['a'] },
    })

    rerender({ ids: ['a', 'b', 'c'] })
    expect([...result.current].sort()).toEqual(['b', 'c'])
  })
})
