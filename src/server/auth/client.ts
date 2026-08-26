import { createAuthClient } from 'better-auth/react'
import { inferAdditionalFields, organizationClient } from 'better-auth/client/plugins'
import { passkeyClient } from '@better-auth/passkey/client'
import { stripeClient } from '@better-auth/stripe/client'

export const authClient = createAuthClient({
  plugins: [
    passkeyClient(),
    organizationClient(),
    stripeClient({ subscription: true }),
    // Mirrors the `user.additionalFields.locale` schema from `src/server/auth/auth.ts` so
    // `authClient.updateUser({ locale })` type-checks on the client.
    inferAdditionalFields({ user: { locale: { type: 'string', required: false, input: true } } }),
  ],
})
