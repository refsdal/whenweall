import { createAuthClient } from 'better-auth/react'
import { inferAdditionalFields } from 'better-auth/client/plugins'
import { passkeyClient } from '@better-auth/passkey/client'

export const authClient = createAuthClient({
  plugins: [
    passkeyClient(),
    // Mirrors the `user.additionalFields.locale` schema from `src/server/auth/auth.ts` so
    // `authClient.updateUser({ locale })` type-checks on the client.
    inferAdditionalFields({ user: { locale: { type: 'string', required: false, input: true } } }),
  ],
})
