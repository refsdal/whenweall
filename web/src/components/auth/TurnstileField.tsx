import { useRouter } from '@tanstack/react-router'
import { Turnstile } from '@marsidev/react-turnstile'
import type { PublicConfig } from '#/api/config'

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
        // No `size` here on purpose: `@marsidev/react-turnstile` applies a fixed inline style to
        // this container based on `options.size` (e.g. `flexible` forces a 65px-tall, 300px-min
        // box) regardless of what the widget actually renders. Production's sitekey is configured
        // as an invisible widget that renders nothing, so that forced box used to sit as a dead
        // empty placeholder in the form. Leaving `size` unset means the container gets no inline
        // sizing at all — it collapses to nothing when the widget renders nothing, and still
        // sizes itself naturally around the dev/e2e test key's visible checkbox widget. The
        // captcha still runs (and solves) exactly as before; only the placeholder box is gone.
        options={{ theme: 'auto' }}
      />
    </div>
  )
}
