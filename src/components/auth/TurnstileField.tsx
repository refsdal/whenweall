import { useRouter } from '@tanstack/react-router'
import { Turnstile } from '@marsidev/react-turnstile'
import type { PublicConfig } from '#/server/config.functions'

/**
 * Reads `publicConfig.turnstileSiteKey` from the root route's `beforeLoad` context.
 *
 * Uses `useRouter({ warn: false })` rather than `useRouteContext` because the latter throws when
 * rendered outside a `<RouterProvider>` (e.g. in a component test that mounts `TurnstileField` on
 * its own) — `useRouter` degrades to `undefined` instead, so the field can render in isolation
 * with an empty site key while still resolving the real one everywhere the app actually renders
 * it. The root context is set once in `beforeLoad`, before this component ever mounts, so a
 * plain (non-reactive) read of `router.state` is safe.
 */
function useTurnstileSiteKey(): string {
  const router = useRouter({ warn: false })
  const rootContext = router?.state.matches[0]?.context as { publicConfig?: PublicConfig }
  return rootContext?.publicConfig?.turnstileSiteKey ?? ''
}

export function TurnstileField({ onToken }: { onToken: (token: string | null) => void }) {
  const siteKey = useTurnstileSiteKey()

  return (
    <div data-slot="turnstile-field">
      <Turnstile
        siteKey={siteKey}
        onSuccess={onToken}
        onExpire={() => onToken(null)}
        onError={() => onToken(null)}
        options={{ theme: 'auto', size: 'flexible' }}
      />
    </div>
  )
}
