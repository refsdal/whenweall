import { Link } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'
import { LogoMark } from '#/components/layout/Logo'
import { m } from '#/lib/i18n'

export function Footer() {
  const year = new Date().getFullYear()

  return (
    <footer className="mt-24 border-t border-border/60">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-4 px-5 py-8 sm:flex-row sm:items-center sm:justify-between sm:px-8">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <LogoMark className="size-4 text-foreground" />
          <span>{m.footer_rights({ year: String(year), name: appConfig.name })}</span>
        </div>

        <div className="flex items-center gap-4 text-sm text-muted-foreground">
          <Link to="/privacy" className="hover:text-foreground">
            {m.footer_privacy()}
          </Link>
          <Link to="/terms" className="hover:text-foreground">
            {m.footer_terms()}
          </Link>
          <LocaleSwitcher />
        </div>
      </div>
    </footer>
  )
}
