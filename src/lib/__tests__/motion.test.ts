import { afterEach, describe, expect, it, vi } from 'vitest'
import { listItem, spring, tapScale } from '#/lib/motion'

const confettiMock = vi.hoisted(() => vi.fn())
vi.mock('canvas-confetti', () => ({ default: confettiMock }))

function mockReducedMotion(reduce: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      matches: reduce && query.includes('prefers-reduced-motion'),
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  )
}

afterEach(() => {
  confettiMock.mockReset()
  vi.unstubAllGlobals()
})

describe('motion tokens', () => {
  it('exposes a spring shared by tap and list transitions', () => {
    expect(spring.type).toBe('spring')
    expect(tapScale.transition).toBe(spring)
    expect(listItem.transition).toBe(spring)
    expect(tapScale.whileTap.scale).toBeLessThan(1)
  })

  it('animates list items in from below and out upwards', () => {
    expect(listItem.initial).toEqual({ opacity: 0, y: 6 })
    expect(listItem.animate).toEqual({ opacity: 1, y: 0 })
    expect(listItem.exit).toEqual({ opacity: 0, y: -6 })
  })
})

describe('celebrate', () => {
  it('does not throw in jsdom and fires confetti', async () => {
    mockReducedMotion(false)
    const { celebrate } = await import('#/lib/confetti')

    expect(() => celebrate('vote')).not.toThrow()
    expect(() => celebrate('finalize')).not.toThrow()
    expect(confettiMock).toHaveBeenCalled()
  })

  it('fires more bursts for a finalize than for a vote', async () => {
    mockReducedMotion(false)
    const { celebrate } = await import('#/lib/confetti')

    celebrate('vote')
    const voteBursts = confettiMock.mock.calls.length
    confettiMock.mockReset()

    celebrate('finalize')
    expect(confettiMock.mock.calls.length).toBeGreaterThan(voteBursts)
  })

  it('does nothing when the user prefers reduced motion', async () => {
    mockReducedMotion(true)
    const { celebrate } = await import('#/lib/confetti')

    celebrate('finalize')
    celebrate('vote')
    expect(confettiMock).not.toHaveBeenCalled()
  })
})
