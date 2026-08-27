import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { AppError } from '#/lib/errors'
import { canManageContent, requireOwnerRole } from '#/server/auth/org-roles'
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

describe('requireOwnerRole', () => {
  it('allows an owner through', () => {
    expect(() => requireOwnerRole('owner')).not.toThrow()
  })
  it('rejects an admin — only the owner manages org-identity settings like the slug (spec §1)', () => {
    expect(() => requireOwnerRole('admin')).toThrow(new AppError('FORBIDDEN'))
  })
  it('rejects a plain member', () => {
    expect(() => requireOwnerRole('member')).toThrow(new AppError('FORBIDDEN'))
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
