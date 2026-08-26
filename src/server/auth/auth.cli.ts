import { APIError, betterAuth } from 'better-auth'
import { organization } from 'better-auth/plugins'
import { drizzleAdapter } from '@better-auth/drizzle-adapter'
import { passkey } from '@better-auth/passkey'
import { stripe } from '@better-auth/stripe'
import { drizzle } from 'drizzle-orm/d1'
import { handleSchema } from '#/server/bookings/schemas'
import { createStripeClient } from '#/server/billing/stripe'
import { PREMIUM_PLAN_NAME } from '#/lib/billing'

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
        // Mirrors the runtime config (auth.ts) for generator parity, even though none of this
        // affects the generated schema (hooks/limits aren't schema-shaping options).
        organizationLimit: 5,
        organizationHooks: {
          beforeCreateOrganization: async ({ organization: org }) => {
            if (org.slug !== undefined && !handleSchema.safeParse(org.slug).success) {
              throw new APIError('BAD_REQUEST', { message: 'Invalid organization slug' })
            }
          },
          beforeUpdateOrganization: async ({ organization: org }) => {
            if (org.slug !== undefined && !handleSchema.safeParse(org.slug).success) {
              throw new APIError('BAD_REQUEST', { message: 'Invalid organization slug' })
            }
          },
          // Mirrors the runtime seat gate (auth.ts's `assertSeatAvailable`) for generator parity;
          // no-op here since this CLI config only exists to shape the generated schema and has no
          // real D1 to query entitlements against.
          beforeCreateInvitation: async () => {},
        },
        // No-op: this CLI config only exists to shape the generated schema.
        sendInvitationEmail: async () => {},
      }),
      stripe({
        // Dummy values: this CLI config only exists to shape the generated schema.
        stripeClient: createStripeClient('sk_test_dummy'),
        stripeWebhookSecret: 'whsec_dummy',
        createCustomerOnSignUp: false,
        subscription: {
          enabled: true,
          plans: [
            {
              name: PREMIUM_PLAN_NAME,
              priceId: 'price_dummy',
              annualDiscountPriceId: 'price_dummy_yearly',
            },
          ],
          authorizeReference: async () => false,
        },
      }),
    ],
    user: {
      additionalFields: {
        locale: { type: 'string', required: false, input: true },
      },
    },
  })
}
export const auth = createAuth(undefined as unknown as D1Database) // CLI only; runtime uses getAuth() (Task 8)
