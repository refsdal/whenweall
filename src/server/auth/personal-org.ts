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
