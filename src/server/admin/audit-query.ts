import type { Db } from '#/server/db/client'
import type { AdminAuditLogRow } from '#/server/db/schema'

/**
 * Reads the audit log, newest first.
 *
 * Deliberately in its own module rather than beside `recordAdminAction`: `audit.ts` must export
 * nothing but the writer and its action list, and a test asserts exactly that. Keeping the read
 * here means adding query helpers can never accidentally introduce a mutating export next to the
 * one thing that must stay append-only.
 */
export async function listAdminAuditLog(db: Db, limit: number): Promise<AdminAuditLogRow[]> {
  return db.query.adminAuditLog.findMany({
    orderBy: (t, { desc }) => [desc(t.createdAt)],
    limit,
  })
}
