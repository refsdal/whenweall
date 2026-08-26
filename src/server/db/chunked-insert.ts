import type { BatchItem } from 'drizzle-orm/batch'
import type { SQLiteTable } from 'drizzle-orm/sqlite-core'
import type { Db } from './client'

/**
 * D1 caps bound parameters per statement at ~100 (SQLITE_ERROR: too many SQL variables). A bulk
 * `.values(rows)` insert whose row count scales with user input (poll options, votes, …) must
 * stay under that no matter how large the caller lets the array grow — so this splits it into
 * multiple `INSERT` statements sized to fit under `maxParams`, given the row's own column count.
 *
 * Returns an array of insert queries meant to be spread into a single `db.batch([...])` call
 * alongside the caller's other statements (D1 runs every statement in one batch/transaction) —
 * it does not execute anything itself. Returns `[]` for an empty `rows` array, since D1 rejects
 * an `INSERT … VALUES` with zero rows.
 */
export function chunkedInsert<T extends SQLiteTable>(
  db: Db,
  table: T,
  rows: T['$inferInsert'][],
  maxParams = 90,
): BatchItem<'sqlite'>[] {
  if (rows.length === 0) return []
  const columnCount = Object.keys(rows[0] as object).length
  const chunkSize = Math.max(1, Math.floor(maxParams / columnCount))
  const queries: BatchItem<'sqlite'>[] = []
  for (let i = 0; i < rows.length; i += chunkSize) {
    queries.push(db.insert(table).values(rows.slice(i, i + chunkSize)) as BatchItem<'sqlite'>)
  }
  return queries
}
