import { and, eq, ne } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { member, organization } from '#/server/db/schema'

/** Lowercased, ascii-folded, hyphen-joined; ≤24 chars so the random suffix fits handleSchema. */
export function slugifyOrgName(name: string): string {
  return name
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/ø/gi, 'o')
    .replace(/æ/gi, 'ae')
    .toLowerCase()
    .replace(/['’]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
    .slice(0, 24)
    .replace(/-+$/, '')
}

const SUFFIX_ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789'
function randomSuffix(len = 6): string {
  const bytes = crypto.getRandomValues(new Uint8Array(len))
  return Array.from(bytes, (b) => SUFFIX_ALPHABET[b % SUFFIX_ALPHABET.length]).join('')
}

/**
 * Every user gets a silent personal organization at signup (spec §1). The slug is auto-generated
 * (editable later in org settings); a random suffix avoids a read-check-insert race on the
 * unique slug column.
 */
export async function createPersonalOrganization(
  db: Db,
  user: { id: string; name: string; email: string },
): Promise<{ orgId: string; slug: string }> {
  const base = slugifyOrgName(user.name) || 'user'
  const slug = `${base}-${randomSuffix()}`
  const orgId = crypto.randomUUID()
  const now = new Date()
  await db.insert(organization).values({ id: orgId, name: user.name, slug, createdAt: now })
  await db.insert(member).values({
    id: crypto.randomUUID(),
    organizationId: orgId,
    userId: user.id,
    role: 'owner',
    createdAt: now,
  })
  return { orgId, slug }
}

/**
 * Deletes every organization where `userId` is the sole `owner`-role member — meant to run right
 * before the user row itself is deleted (`databaseHooks.user.delete.before`). Content ownership
 * lives on the organization now (v4 tenancy): `polls`/`bookingPages` cascade from `organization`,
 * not from `user`, so with no owner left to speak for it an org (most commonly someone's personal
 * one) would otherwise survive its departed owner forever, along with every poll and booking page
 * under it — including a still-live, still-bookable public booking page nobody can manage anymore.
 *
 * An org with another `owner`-role member survives untouched, including this user's own content in
 * it (`createdBy`/`memberUserId` simply go `null` via their own `set null` FKs). This user's
 * `member` row — here and in every other org they belonged to — is left for `member.userId`'s own
 * `onDelete: 'cascade'` to clean up once the user row is actually deleted just after this hook
 * returns; deleting it here too would be redundant.
 */
export async function deleteOrphanedOwnerOrganizations(db: Db, userId: string): Promise<void> {
  const ownedMemberships = await db.query.member.findMany({
    where: and(eq(member.userId, userId), eq(member.role, 'owner')),
  })

  for (const membership of ownedMemberships) {
    const otherOwner = await db.query.member.findFirst({
      where: and(
        eq(member.organizationId, membership.organizationId),
        eq(member.role, 'owner'),
        ne(member.userId, userId),
      ),
    })
    if (!otherOwner) {
      await db.delete(organization).where(eq(organization.id, membership.organizationId))
    }
  }
}
