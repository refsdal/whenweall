/** The Stripe plan name for Premium, as configured in the `stripe` plugin's `subscription.plans`
 * (see `src/server/auth/auth.ts` / `auth.cli.ts`) and stored verbatim in `subscription.plan`
 * rows. Lives in this tiny client-safe module — rather than `entitlements.ts`, which pulls in
 * drizzle-orm and D1 types — so client components (e.g. `BillingSection.tsx`) can import it
 * without dragging server-only code into the browser bundle. */
export const PREMIUM_PLAN_NAME = 'premium'
