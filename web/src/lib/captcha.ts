import { useRouter } from '@tanstack/react-router'
import type { PublicConfig } from '#/api/config'

/**
 * Reads `publicConfig.turnstileSiteKey` from the root route's `beforeLoad` context.
 *
 * Uses `useRouter({ warn: false })` rather than `useRouteContext` because the latter throws when
 * rendered outside a `<RouterProvider>` (e.g. in a component test that mounts a form on its own)
 * — `useRouter` degrades to `undefined` instead, so forms can render in isolation. The root
 * context is set once in `beforeLoad`, before any form mounts, so a plain (non-reactive) read of
 * `router.state` is safe. An empty string means the deployment has no Turnstile configured
 * (`GET /api/v1/config` omits `turnstileSiteKey` when the TURNSTILE_* pair is unset).
 */
export function useTurnstileSiteKey(): string {
  const router = useRouter({ warn: false })
  const rootContext = router?.state.matches[0]?.context as { publicConfig?: PublicConfig } | undefined
  return rootContext?.publicConfig?.turnstileSiteKey ?? ''
}

/**
 * Whether this deployment asks for a captcha at all. Every captcha gate in the app must be
 * `captchaEnabled && !captchaToken`: the Go side only verifies `X-Captcha-Token` when
 * `cfg.Capabilities.Turnstile` is on (internal/httpserver's RequireCaptchaIfAnon and
 * authCaptchaMiddleware), so a UI that demanded a token on an instance without keys would be
 * unusable for nothing (spec §8: an unset capability is invisible, never broken).
 */
export function useCaptchaEnabled(): boolean {
  return useTurnstileSiteKey() !== ''
}
