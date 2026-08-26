import { describe, expect, it } from 'vitest'
import { slugifyOrgName } from '#/server/auth/personal-org'
import { LIMITS, handleSchema } from '#/server/bookings/schemas'

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
  it('truncates to LIMITS.handleMax - 7 chars without trailing hyphen (room for "-" + a 6-char suffix)', () => {
    expect(slugifyOrgName('a'.repeat(40)).length).toBeLessThanOrEqual(LIMITS.handleMax - 7)
  })
  it('leaves room for createPersonalOrganization\'s "-" + 6-char suffix to still pass handleSchema, even for a long name', () => {
    const base = slugifyOrgName('Christopher Alexander Ng')
    const withSuffix = `${base}-abc123`
    expect(withSuffix.length).toBeLessThanOrEqual(LIMITS.handleMax)
    expect(handleSchema.safeParse(withSuffix).success).toBe(true)
  })
})
