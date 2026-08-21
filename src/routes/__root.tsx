import {
  HeadContent,
  Outlet,
  Scripts,
  createRootRoute,
  type ErrorComponentProps,
} from '@tanstack/react-router'
import { useEffect } from 'react'
import { MotionConfig } from 'motion/react'
import appCss from '../styles.css?url'
import { appConfig } from '#/app.config'
import { ErrorCard } from '#/components/layout/ErrorCard'
import { Header } from '#/components/layout/Header'
import { Footer } from '#/components/layout/Footer'
import { NotFoundCard } from '#/components/layout/NotFoundCard'
import { Toaster } from '#/components/ui/sonner'
import { getLocale, m } from '#/lib/i18n'
import { themeInitScript } from '#/lib/theme'
import { getSession } from '#/server/auth/session.functions'
import { getPublicConfig } from '#/server/config.functions'

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: 'utf-8' },
      { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      { title: `${appConfig.name} — ${appConfig.tagline}` },
      { name: 'description', content: appConfig.description },
      {
        name: 'theme-color',
        content: appConfig.brand.paperLight,
        media: '(prefers-color-scheme: light)',
      },
      {
        name: 'theme-color',
        content: appConfig.brand.paperDark,
        media: '(prefers-color-scheme: dark)',
      },
    ],
    links: [
      { rel: 'stylesheet', href: appCss },
      { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' },
    ],
  }),
  beforeLoad: async () => ({
    session: await getSession(),
    locale: getLocale(),
    publicConfig: await getPublicConfig(),
  }),
  notFoundComponent: RootNotFound,
  errorComponent: RootError,
  shellComponent: RootDocument,
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

function RootDocument({ children }: { children: React.ReactNode }) {
  // `shellComponent` renders outside the matched route tree, so it has no access to
  // `Route.useRouteContext()`. Paraglide's `getLocale()` resolves correctly here in both
  // environments: on the server from the `paraglideMiddleware` async context, on the client
  // from the `samla_locale` cookie / document.
  const locale = getLocale()

  return (
    <html lang={locale} suppressHydrationWarning>
      <head>
        {/* Resolves the stored theme before first paint so the page never flashes. */}
        <script dangerouslySetInnerHTML={{ __html: themeInitScript }} />
        <HeadContent />
      </head>
      <body className="min-h-dvh antialiased">
        {children}
        <Scripts />
      </body>
    </html>
  )
}
