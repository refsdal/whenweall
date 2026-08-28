import { describe, expect, it } from 'vitest'
import { isStaff, requireStaff } from '#/server/auth/platform-roles'
import { errorCode } from '#/lib/errors'

describe('isStaff', () => {
  it('accepts the staff role', () => {
    expect(isStaff({ role: 'staff' })).toBe(true)
  })

  it('rejects an ordinary user, a missing role and a null role', () => {
    expect(isStaff({ role: 'user' })).toBe(false)
    expect(isStaff({})).toBe(false)
    expect(isStaff({ role: null })).toBe(false)
  })

  // 'admin' is an OrgRole on `member`, not a platform role. If it ever satisfied this predicate,
  // every organisation admin would silently become a system administrator.
  it('rejects the org role "admin"', () => {
    expect(isStaff({ role: 'admin' })).toBe(false)
  })

  // The most important assertion in this phase. Without it, an admin could impersonate a user and
  // keep their own admin powers while acting as that user — a privilege escalation and an
  // audit-trail hole at the same time.
  it('rejects a staff session that is currently impersonating someone', () => {
    expect(isStaff({ role: 'staff', impersonatedBy: 'user_123' })).toBe(false)
  })
})

describe('requireStaff', () => {
  it('throws FORBIDDEN for a non-staff session', () => {
    let caught: unknown
    try {
      requireStaff({ role: 'user' })
    } catch (err) {
      caught = err
    }
    expect(errorCode(caught)).toBe('FORBIDDEN')
  })

  it('throws FORBIDDEN for staff while impersonating', () => {
    let caught: unknown
    try {
      requireStaff({ role: 'staff', impersonatedBy: 'user_123' })
    } catch (err) {
      caught = err
    }
    expect(errorCode(caught)).toBe('FORBIDDEN')
  })

  it('returns quietly for staff', () => {
    expect(() => requireStaff({ role: 'staff' })).not.toThrow()
  })
})
