import { createServerFn } from '@tanstack/react-start'
import * as z from 'zod'
import { getDb } from '#/server/db/client'
import { requireStaffMiddleware } from '#/server/auth/staff'
import { getAdminStats, type AdminStats } from './stats'
import {
  getAdminUserDetail,
  listAdminUsers,
  type AdminUserDetail,
  type AdminUserSummary,
} from './users'

/*
 * Same "declare once, reuse for the function and the manifest" convention as
 * `polls.functions.ts` — see the comment there.
 */
const REQUIRE_STAFF = [requireStaffMiddleware] as const

export const SERVER_FN_MIDDLEWARE = {
  fetchAdminStats: REQUIRE_STAFF,
  fetchAdminUsers: REQUIRE_STAFF,
  fetchAdminUserDetail: REQUIRE_STAFF,
} as const

export const fetchAdminStats = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.fetchAdminStats)
  .handler(async (): Promise<AdminStats> => getAdminStats(getDb()))

const listQuerySchema = z.object({
  search: z.string().max(200).optional(),
  limit: z.number().int().min(1).max(100).default(50),
  offset: z.number().int().min(0).default(0),
})

export const fetchAdminUsers = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.fetchAdminUsers)
  .validator(listQuerySchema)
  .handler(async ({ data }): Promise<{ users: AdminUserSummary[]; total: number }> =>
    listAdminUsers(getDb(), data),
  )

export const fetchAdminUserDetail = createServerFn({ method: 'GET' })
  .middleware(SERVER_FN_MIDDLEWARE.fetchAdminUserDetail)
  .validator(z.object({ userId: z.string().min(1).max(128) }))
  .handler(async ({ data }): Promise<AdminUserDetail | null> =>
    getAdminUserDetail(getDb(), data.userId),
  )
