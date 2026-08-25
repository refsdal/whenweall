import { betterAuth } from 'better-auth'
import { organization } from 'better-auth/plugins'
import { drizzleAdapter } from '@better-auth/drizzle-adapter'
import { passkey } from '@better-auth/passkey'
import { drizzle } from 'drizzle-orm/d1'

// This shape exists so `bun run auth:generate` can emit the schema.
// The runtime auth config (getAuth()) is created in Task 8's src/server/auth/auth.ts,
// which must not be confused with this CLI-only file.
export function createAuth(d1: D1Database) {
  return betterAuth({
    database: drizzleAdapter(drizzle(d1), { provider: 'sqlite' }),
    emailAndPassword: { enabled: true },
    plugins: [
      passkey(),
      organization({
        creatorRole: 'owner',
        // No-op: this CLI config only exists to shape the generated schema.
        sendInvitationEmail: async () => {},
      }),
    ],
    user: {
      additionalFields: {
        locale: { type: 'string', required: false, input: true },
        handle: { type: 'string', required: false, input: false },
      },
    },
  })
}
export const auth = createAuth(undefined as unknown as D1Database) // CLI only; runtime uses getAuth() (Task 8)
