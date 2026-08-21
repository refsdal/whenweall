import { useRouteContext } from '@tanstack/react-router'
import { appConfig, type AppLocale } from '#/app.config'
import { getLocale, m, setLocale } from '#/lib/i18n'
import { authClient } from '#/server/auth/client'

const LOCALE_LABELS: Record<AppLocale, () => string> = {
  en: m.locale_en,
  nb: m.locale_nb,
}

export function LocaleSwitcher() {
  const { session } = useRouteContext({ from: '__root__' })
  const activeLocale = getLocale()

  function handleSelect(locale: AppLocale) {
    if (locale === activeLocale) return

    if (session) {
      void authClient.updateUser({ locale })
    }

    // Sets the `samla_locale` cookie and reloads the page by default.
    setLocale(locale)
  }

  return (
    <div role="group" aria-label={m.locale_switch_label()} className="inline-flex gap-1">
      {appConfig.locales.map((locale) => (
        <button
          key={locale}
          type="button"
          aria-pressed={locale === activeLocale}
          onClick={() => handleSelect(locale)}
          className="rounded-full border border-[var(--fg)]/20 px-3 py-1 text-sm font-medium transition-colors aria-pressed:bg-[var(--fg)] aria-pressed:text-[var(--bg)]"
        >
          {LOCALE_LABELS[locale]()}
        </button>
      ))}
    </div>
  )
}
