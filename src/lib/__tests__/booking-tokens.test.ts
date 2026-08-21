import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearBookingToken, loadBookingToken, saveBookingToken } from '#/lib/booking-tokens'

afterEach(() => {
  window.localStorage.clear()
  vi.restoreAllMocks()
})

describe('booking tokens', () => {
  it('round-trips a manage token', () => {
    saveBookingToken('abcdefghijkl', 'tok_1')

    expect(loadBookingToken('abcdefghijkl')).toBe('tok_1')
  })

  it('stores each booking under its own key', () => {
    saveBookingToken('abcdefghijkl', 'tok_1')
    saveBookingToken('mnopqrstuvwx', 'tok_2')

    expect(window.localStorage.getItem('samla:booking:abcdefghijkl')).toBe('tok_1')
    expect(loadBookingToken('mnopqrstuvwx')).toBe('tok_2')
  })

  it('returns null when nothing is stored', () => {
    expect(loadBookingToken('abcdefghijkl')).toBeNull()
  })

  it('clears a stored token', () => {
    saveBookingToken('abcdefghijkl', 'tok_1')
    clearBookingToken('abcdefghijkl')

    expect(loadBookingToken('abcdefghijkl')).toBeNull()
  })

  it('never throws when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('blocked')
    })

    expect(() => saveBookingToken('abcdefghijkl', 'tok_1')).not.toThrow()
    expect(loadBookingToken('abcdefghijkl')).toBeNull()
    expect(() => clearBookingToken('abcdefghijkl')).not.toThrow()
  })
})
