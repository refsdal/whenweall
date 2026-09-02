import { Outlet, createRootRoute, type ErrorComponentProps } from '@tanstack/react-router'
import { useEffect } from 'react'
import { MotionConfig } from 'motion/react'
import { ErrorCard } from '#/components/layout/ErrorCard'
import { Header } from '#/components/layout/Header'
import { Footer } from '#/components/layout/Footer'
import { NotFoundCard } from '#/components/layout/NotFoundCard'
import { RouteProgress } from '#/components/layout/RouteProgress'
import { Toaster } from '#/components/ui/sonner'
import { getLocale, m } from '#/lib/i18n'
import { getSession } from '#/server/auth/session.functions'
import { getPublicConfig } from '#/server/config.functions'

// NOTE(go-rewrite-08 task 1): `head`/`shellComponent`/`HeadContent`/`Scripts` were TanStack
// Start SSR-only APIs — there is no equivalent in plain `@tanstack/react-router` (no server, no
// document to render). The document shell they used to produce (`<html>`, the theme-init
// script, `<head>` meta, `#root` mount point) now lives statically in `web/index.html`, which
// Vite serves and injects `main.tsx` into directly. `beforeLoad`'s `getSession`/`getPublicConfig`
// calls are TanStack Start server functions (`#/server/*`) that no longer resolve under `web/` —
// left as-is on purpose; replacing them with real Go API calls is Task 2-4's job (see
// worklist.txt), not this move.
export const Route = createRootRoute({
  beforeLoad: async () => ({
    session: await getSession(),
    locale: getLocale(),
    publicConfig: await getPublicConfig(),
  }),
  notFoundComponent: RootNotFound,
  errorComponent: RootError,
  component: RootLayout,
})

function RootNotFound() {
  return (
    <NotFoundCard
      title={m.error_404_title()}
      body={m.error_404_body()}
      ctaLabel={m.error_404_cta()}
    />
  )
}

function RootError({ error, reset }: ErrorComponentProps) {
  return <ErrorCard error={error} onRetry={reset} />
}

/**
 * The chrome around every page. Lives in `component` rather than `shellComponent` so that
 * `Header`/`Footer` can read the root route context (session, locale).
 */
function RootLayout() {
  // Marks the document as hydrated so e2e tests can wait before typing into SSR'd inputs
  // (a controlled input typed into before React attaches would be reset on hydration).
  useEffect(() => {
    document.documentElement.dataset.hydrated = 'true'
  }, [])

  return (
    <MotionConfig reducedMotion="user">
      <div className="flex min-h-dvh flex-col">
        <RouteProgress />
        <Header />
        <main className="flex-1">
          <Outlet />
        </main>
        <Footer />
        <Toaster position="bottom-right" />
      </div>
    </MotionConfig>
  )
}
