import type { Db } from '#/server/db/client'
import { adminAuditLog } from '#/server/db/schema'
import { newId } from '#/lib/ids'

/**
 * The `/admin/*` endpoint suffixes that change something and must leave a trail.
 *
 * Read-only endpoints (`list-users`, `get-user`, `list-user-sessions`, `has-permission`) are
 * deliberately absent: auditing a list view produces volume that buries the handful of entries
 * anyone actually needs to find.
 */
export const AUDITED_ADMIN_ACTIONS = [
  'set-role',
  'create-user',
  'update-user',
  'set-user-password',
  'remove-user',
  'ban-user',
  'unban-user',
  'impersonate-user',
  'stop-impersonating',
  'revoke-user-session',
  'revoke-user-sessions',
] as const

export type AuditedAdminAction = (typeof AUDITED_ADMIN_ACTIONS)[number]

export function isAuditedAdminAction(action: string): action is AuditedAdminAction {
  return (AUDITED_ADMIN_ACTIONS as readonly string[]).includes(action)
}

/**
 * Appends one row to the audit log.
 *
 * There is intentionally no counterpart that edits or removes a row — see the table's own doc
 * comment in `schema.ts`. A test asserts this module exports nothing that could.
 */
export async function recordAdminAction(
  db: Db,
  entry: {
    actorUserId: string
    actorEmail: string
    action: string
    targetType: 'user' | 'org' | 'poll'
    targetId: string | null
    reason: string | null
    metadata?: Record<string, unknown> | null
  },
): Promise<void> {
  await db.insert(adminAuditLog).values({
    id: newId(),
    actorUserId: entry.actorUserId,
    actorEmail: entry.actorEmail,
    action: entry.action,
    targetType: entry.targetType,
    targetId: entry.targetId,
    reason: entry.reason,
    metadata: entry.metadata ? JSON.stringify(entry.metadata) : null,
    createdAt: new Date().toISOString(),
  })
}
