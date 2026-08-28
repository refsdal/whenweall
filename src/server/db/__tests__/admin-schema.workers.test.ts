import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { eq } from 'drizzle-orm'
import { createDb } from '#/server/db/client'
import { adminAuditLog, user } from '#/server/db/schema'
import { newId } from '#/lib/ids'
import { makeUser } from '../../../../test/helpers'

describe('admin_audit_log', () => {
  // The trail has to outlive the person it incriminates, so the FK is `set null` rather than
  // `cascade` and the actor's email is denormalised at write time.
  it('keeps the row, and the actor email, after the actor is deleted', async () => {
    const db = createDb(env.DB)
    const { id: actorId, email } = await makeUser(db)
    const rowId = newId()

    await db.insert(adminAuditLog).values({
      id: rowId,
      actorUserId: actorId,
      actorEmail: email,
      action: 'impersonate-user',
      targetType: 'user',
      targetId: 'target-1',
      reason: 'support ticket 12',
      metadata: null,
      createdAt: new Date().toISOString(),
    })

    await db.delete(user).where(eq(user.id, actorId))

    const row = await db.query.adminAuditLog.findFirst({ where: eq(adminAuditLog.id, rowId) })
    expect(row).toBeDefined()
    expect(row!.actorUserId).toBeNull()
    expect(row!.actorEmail).toBe(email)
    expect(row!.action).toBe('impersonate-user')
  })
})
