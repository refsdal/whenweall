import { and, eq, inArray } from 'drizzle-orm'
import { resolveChannels, type NotificationEvent } from '#/lib/notifications'
import { getEntitlements } from '#/server/billing/entitlements'
import type { Db } from '#/server/db/client'
import {
  member,
  notificationPrefs,
  notificationSubscriptions,
  user,
  type ScopeType,
} from '#/server/db/schema'

export type NotificationScope = { type: ScopeType; id: string; organizationId: string }
export type Recipient = { userId: string; email: string; name: string; locale: string }
export type ResolvedRecipients = { email: Recipient[]; push: Recipient[] }

/**
 * Turns a scope + event into the two channel lists.
 *
 * Membership is the authority, not the subscription row: someone who has left the org (or lost
 * their seat) can no longer open the poll, so they must not keep hearing about it. Their row is
 * left in place so re-adding them restores their settings rather than silently resetting them.
 */
export async function resolveRecipients(
  db: Db,
  scope: NotificationScope,
  event: NotificationEvent,
  opts: { actorUserId?: string | null } = {},
): Promise<ResolvedRecipients> {
  const out: ResolvedRecipients = { email: [], push: [] }

  const subscriptions = await db.query.notificationSubscriptions.findMany({
    where: and(
      eq(notificationSubscriptions.scopeType, scope.type),
      eq(notificationSubscriptions.scopeId, scope.id),
    ),
  })

  const actorUserId = opts.actorUserId ?? null
  const candidates = subscriptions.filter((s) => s.userId !== actorUserId)
  if (candidates.length === 0) return out

  const candidateIds = candidates.map((s) => s.userId)

  const [members, users, prefs, entitlements] = await Promise.all([
    db.query.member.findMany({
      where: and(
        eq(member.organizationId, scope.organizationId),
        inArray(member.userId, candidateIds),
      ),
    }),
    db.query.user.findMany({ where: inArray(user.id, candidateIds) }),
    db.query.notificationPrefs.findMany({
      where: inArray(notificationPrefs.userId, candidateIds),
    }),
    getEntitlements(db, scope.organizationId),
  ])

  const memberIds = new Set(members.map((m) => m.userId))
  const usersById = new Map(users.map((u) => [u.id, u]))
  const prefsByUser = new Map(prefs.map((p) => [p.userId, p.channels ?? null]))

  for (const subscription of candidates) {
    if (!memberIds.has(subscription.userId)) continue

    const u = usersById.get(subscription.userId)
    if (!u?.email) continue

    const channels = resolveChannels(
      event,
      subscription.channels ?? null,
      prefsByUser.get(subscription.userId) ?? null,
    )
    const recipient: Recipient = {
      userId: u.id,
      email: u.email,
      name: u.name,
      locale: u.locale ?? 'en',
    }

    if (channels.email) out.email.push(recipient)
    // Push is Premium-only. Gated here rather than at send time so a lapsed subscription stops
    // pushing everywhere at once; the device rows are left alone so re-upgrading needs no new
    // browser permission prompt.
    if (channels.push && entitlements.push) out.push.push(recipient)
  }

  return out
}
