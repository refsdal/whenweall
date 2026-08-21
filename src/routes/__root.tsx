import { HeadContent, Outlet, Scripts, createRootRoute } from '@tanstack/react-router'
import appCss from '../styles.css?url'
import { appConfig } from '#/app.config'
import { getLocale } from '#/lib/i18n'
import { getSession } from '#/server/auth/session.functions'

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: 'utf-8' },
      { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      { title: appConfig.name },
      { name: 'description', content: appConfig.description },
    ],
    links: [{ rel: 'stylesheet', href: appCss }],
  }),
  beforeLoad: async () => ({ session: await getSession(), locale: getLocale() }),
  shellComponent: RootDocument,
  component: () => <Outlet />,
})

function RootDocument({ children }: { children: React.ReactNode }) {
  // `shellComponent` renders outside the matched route tree, so it has no access to
  // `Route.useRouteContext()`. Paraglide's `getLocale()` resolves correctly here in both
  // environments: on the server from the `paraglideMiddleware` async context, on the client
  // from the `samla_locale` cookie / document.
  const locale = getLocale()

  return (
    <html lang={locale} suppressHydrationWarning>
      <head>
        <HeadContent />
      </head>
      <body className="min-h-dvh bg-[var(--bg)] text-[var(--fg)] antialiased">
        {children}
        <Scripts />
      </body>
    </html>
  )
}
