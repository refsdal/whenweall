import { api, ApiError } from '#/api/client'
import type { AdminStats, AdminUserDetail, AdminUserRow, AuditEntry, FailedJobView } from '#/api/types'

/**
 * The staff-only console's client, against internal/admin/handlers.go's REST surface. Shape
 * changes vs the old (deleted) `admin.functions.ts` — carried from the plan-8 dispatch note:
 *
 * - stats: flattened (no `growth`/`revenue` nesting), and `revenue` is GONE (billing removed).
 * - users list: `{users, total, nextCursor}`, cursor-paginated (not offset).
 * - a user row: `staff: boolean` (not `role === 'staff'`), `locked`/`lockReason` (not
 *   `banned`/`banReason`), `orgs[].roles: string[]` (not a single `role`).
 * - audit: `{entries, total, nextCursor}`, filterable by `targetType`/`targetId`/`action`/`actor`,
 *   cursor-paginated; a user's own recent actions are fetched by filtering this same endpoint
 *   (`targetType: 'user', targetId: id`) — `AdminUserDetail` no longer carries `recentActions`.
 */

export function fetchAdminStats(): Promise<AdminStats> {
  return api<AdminStats>('GET', '/api/v1/admin/stats')
}

export type UsersPage = { users: AdminUserRow[]; total: number; nextCursor: string | null }

export function fetchAdminUsers(params: {
  query?: string
  cursor?: string
  limit?: number
}): Promise<UsersPage> {
  return api<UsersPage>('GET', '/api/v1/admin/users', undefined, {
    query: { query: params.query, cursor: params.cursor, limit: params.limit },
  })
}

export async function fetchAdminUserDetail(userId: string): Promise<AdminUserDetail | null> {
  try {
    return await api<AdminUserDetail>('GET', `/api/v1/admin/users/${userId}`)
  } catch (err) {
    if (err instanceof ApiError && err.code === 'not_found') return null
    throw err
  }
}

export async function lockUser(userId: string, reason: string): Promise<void> {
  await api('POST', `/api/v1/admin/users/${userId}/lock`, { reason })
}

export async function unlockUser(userId: string, reason: string): Promise<void> {
  await api('POST', `/api/v1/admin/users/${userId}/unlock`, { reason })
}

export async function deleteAdminUser(userId: string, reason: string): Promise<void> {
  await api('DELETE', `/api/v1/admin/users/${userId}`, { reason })
}

export type AuditPage = { entries: AuditEntry[]; total: number; nextCursor: string | null }

export function fetchAuditLog(params: {
  action?: string
  actor?: string
  targetType?: string
  targetId?: string
  cursor?: string
  limit?: number
}): Promise<AuditPage> {
  return api<AuditPage>('GET', '/api/v1/admin/audit', undefined, {
    query: {
      action: params.action,
      actor: params.actor,
      targetType: params.targetType,
      targetId: params.targetId,
      cursor: params.cursor,
      limit: params.limit,
    },
  })
}

export function fetchFailedJobs(): Promise<FailedJobView[]> {
  return api<{ jobs: FailedJobView[] }>('GET', '/api/v1/admin/jobs/failed').then((r) => r.jobs)
}

export async function retryJob(jobId: string): Promise<void> {
  await api('POST', `/api/v1/admin/jobs/${jobId}/retry`)
}
