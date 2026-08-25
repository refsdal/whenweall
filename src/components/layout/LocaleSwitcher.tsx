import { useRouteContext } from '@tanstack/react-router'
import { appConfig, type AppLocale } from '#/app.config'
import { getLocale, m, setLocale } from '#/lib/i18n'
import { authClient } from '#/server/auth/client'
import { cn } from '#/lib/utils'

const LOCALE_LABELS: Record<AppLocale, () => string> = {
  en: m.locale_en,
  nb: m.locale_nb,
}

export function LocaleSwitcher({ className }: { className?: string }) {
  const { session } = useRouteContext({ from: '__root__' })
  const activeLocale = getLocale()

  function handleSelect(locale: AppLocale) {
    if (locale === activeLocale) return

    if (session) {
      void authClient.updateUser({ locale })
    }

    // Sets the `whenweall_locale` cookie and reloads the page by default.
    setLocale(locale)
  }

  return (
    <div
      role="group"
      aria-label={m.locale_switch_label()}
      className={cn(
        'inline-flex items-center gap-0.5 rounded-full border border-border/70 p-0.5',
        className,
      )}
    >
      {appConfig.locales.map((locale) => (
        <button
          key={locale}
          type="button"
          aria-pressed={locale === activeLocale}
          onClick={() => handleSelect(locale)}
          className={cn(
            'focus-ring rounded-full px-2.5 py-1 text-xs font-medium tracking-wide text-muted-foreground uppercase transition-colors',
            'hover:text-foreground aria-pressed:bg-secondary aria-pressed:text-foreground',
          )}
        >
          {LOCALE_LABELS[locale]()}
        </button>
      ))}
    </div>
  )
}
