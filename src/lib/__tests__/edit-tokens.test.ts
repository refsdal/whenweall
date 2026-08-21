import { afterEach, describe, expect, it, vi } from 'vitest'
import { clearEditToken, loadEditToken, saveEditToken } from '#/lib/edit-tokens'

afterEach(() => {
  window.localStorage.clear()
  vi.restoreAllMocks()
})

describe('edit tokens', () => {
  it('round-trips a participant id and token', () => {
    saveEditToken('abcdefghijkl', 'pa_1', 'tok_1')

    expect(loadEditToken('abcdefghijkl')).toEqual({ participantId: 'pa_1', token: 'tok_1' })
  })

  it('stores each poll under its own key', () => {
    saveEditToken('abcdefghijkl', 'pa_1', 'tok_1')
    saveEditToken('mnopqrstuvwx', 'pa_2', 'tok_2')

    expect(window.localStorage.getItem('samla:edit:abcdefghijkl')).toBeTruthy()
    expect(loadEditToken('mnopqrstuvwx')).toEqual({ participantId: 'pa_2', token: 'tok_2' })
  })

  it('returns null when nothing is stored', () => {
    expect(loadEditToken('abcdefghijkl')).toBeNull()
  })

  it('returns null for unreadable or malformed entries', () => {
    window.localStorage.setItem('samla:edit:abcdefghijkl', 'not json')
    expect(loadEditToken('abcdefghijkl')).toBeNull()

    window.localStorage.setItem('samla:edit:abcdefghijkl', JSON.stringify({ participantId: 'x' }))
    expect(loadEditToken('abcdefghijkl')).toBeNull()
  })

  it('clears a stored token', () => {
    saveEditToken('abcdefghijkl', 'pa_1', 'tok_1')
    clearEditToken('abcdefghijkl')

    expect(loadEditToken('abcdefghijkl')).toBeNull()
  })

  it('never throws when storage is unavailable', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })

    expect(() => saveEditToken('abcdefghijkl', 'pa_1', 'tok_1')).not.toThrow()
    expect(loadEditToken('abcdefghijkl')).toBeNull()
  })
})
