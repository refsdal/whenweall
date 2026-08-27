import { and, eq } from 'drizzle-orm'
import type { NotificationGrid } from '#/lib/notifications'
import type { Db } from '#/server/db/client'
import { notificationSubscriptions, type ScopeType } from '#/server/db/schema'

export type SubscriptionScope = { type: ScopeType; id: string }

const scopeWhere = (scope: SubscriptionScope) =>
  and(
    eq(notificationSubscriptions.scopeType, scope.type),
    eq(notificationSubscriptions.scopeId, scope.id),
  )

const rowWhere = (scope: SubscriptionScope, userId: string) =>
  and(scopeWhere(scope), eq(notificationSubscriptions.userId, userId))

/**
 * Called when a poll or booking page is created. `userId` is nullable because `createdBy` is — a
 * poll whose creator later deletes their account has nobody to subscribe, and that is not an
 * error.
 */
export async function ensureCreatorSubscription(
  db: Db,
  scope: SubscriptionScope,
  userId: string | null,
): Promise<void> {
  if (!userId) return
  await upsert(db, scope, userId, 'creator')
}

export async function followScope(db: Db, scope: SubscriptionScope, userId: string): Promise<void> {
  await upsert(db, scope, userId, 'follow')
}

export async function unfollowScope(
  db: Db,
  scope: SubscriptionScope,
  userId: string,
): Promise<void> {
  await db.delete(notificationSubscriptions).where(rowWhere(scope, userId))
}

/** `null` clears the override so the row falls back to the user's defaults again. */
export async function setScopeChannels(
  db: Db,
  scope: SubscriptionScope,
  userId: string,
  channels: NotificationGrid | null,
): Promise<void> {
  await db
    .update(notificationSubscriptions)
    .set({ channels, updatedAt: new Date().toISOString() })
    .where(rowWhere(scope, userId))
}

/**
 * The manual replacement for the foreign-key cascade a polymorphic `scopeId` cannot have. Call
 * from every path that removes a poll or a booking page.
 */
export async function deleteScopeSubscriptions(db: Db, scope: SubscriptionScope): Promise<void> {
  await db.delete(notificationSubscriptions).where(scopeWhere(scope))
}

async function upsert(
  db: Db,
  scope: SubscriptionScope,
  userId: string,
  source: 'creator' | 'follow',
): Promise<void> {
  const now = new Date().toISOString()
  await db
    .insert(notificationSubscriptions)
    .values({
      scopeType: scope.type,
      scopeId: scope.id,
      userId,
      source,
      channels: null,
      createdAt: now,
      updatedAt: now,
    })
    // Re-following must not reset an override the user already tuned, so a conflict is a no-op
    // rather than an overwrite.
    .onConflictDoNothing()
}
