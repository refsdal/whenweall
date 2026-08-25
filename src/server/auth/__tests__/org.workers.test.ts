import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { canManageContent } from '#/server/auth/org'
import { makeOrg, makeUser } from '../../../../test/helpers'

describe('canManageContent', () => {
  it('lets creators manage their own content', () => {
    expect(canManageContent({ role: 'member' }, 'u1', 'u1')).toBe(true)
  })
  it('blocks members from others content', () => {
    expect(canManageContent({ role: 'member' }, 'u1', 'u2')).toBe(false)
  })
  it('lets admin and owner manage all org content', () => {
    expect(canManageContent({ role: 'admin' }, 'u1', 'u2')).toBe(true)
    expect(canManageContent({ role: 'owner' }, 'u1', null)).toBe(true)
  })
})

describe('makeOrg helper', () => {
  it('creates an org with an owner membership', async () => {
    const db = createDb(env.DB)
    const u = await makeUser(db)
    const org = await makeOrg(db, u.id)
    expect(org.id).toBeTruthy()
    expect(org.slug).toBeTruthy()
  })
})
