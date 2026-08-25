import { describe, expect, it } from 'vitest'
import { slugifyOrgName } from '#/server/auth/personal-org'

describe('slugifyOrgName', () => {
  it('lowercases, strips diacritics and joins with hyphens', () => {
    expect(slugifyOrgName('Anders Refsdal Olsen')).toBe('anders-refsdal-olsen')
    expect(slugifyOrgName('Åse Bø')).toBe('ase-bo')
  })
  it('drops characters outside [a-z0-9-] and collapses hyphens', () => {
    expect(slugifyOrgName("O'Brien & Sons!!")).toBe('obrien-sons')
  })
  it('returns empty string when nothing survives', () => {
    expect(slugifyOrgName('日本語')).toBe('')
  })
  it('truncates to 24 chars without trailing hyphen', () => {
    expect(slugifyOrgName('a'.repeat(40)).length).toBeLessThanOrEqual(24)
  })
})
