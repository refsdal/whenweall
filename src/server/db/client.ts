import { drizzle } from 'drizzle-orm/d1'
import { env } from 'cloudflare:workers'
import * as schema from './schema'

export function createDb(d1: D1Database) {
  return drizzle(d1, { schema })
}
export type Db = ReturnType<typeof createDb>
export function getDb(): Db {
  return createDb(env.DB)
}
