import { appConfig, type AppLocale } from '#/app.config'
import { getLocale, m, setLocale } from '#/lib/i18n'
import { useSession } from '#/lib/use-session'
import { cn } from '#/lib/utils'
import { updateProfile } from '#/api/auth'

const LOCALE_LABELS: Record<AppLocale, () => string> = {
  en: m.locale_en,
  nb: m.locale_nb,
}

export function LocaleSwitcher({ className }: { className?: string }) {
  const session = useSession()
  const activeLocale = getLocale()

  async function handleSelect(locale: AppLocale) {
    if (locale === activeLocale) return

    // Signed in: persist first (PATCH /api/v1/me writes user_preferences.locale, which every mail
    // to this user renders in), THEN switch the cookie — setLocale reloads the page, and a
    // fire-and-forget request would be aborted by that navigation. Works for unverified users
    // too (the route allows it), so the resent verification mail comes in the new language. A
    // failure is not worth blocking the UI switch over.
    if (session) {
      try {
        await updateProfile({ locale })
      } catch {
        // cookie-only switch still happens below
      }
    }
    // Sets the `whenweall_locale` cookie and reloads the page by default.
    setLocale(locale)
  }

  return (
    <div
      role="group"
      aria-label={m.locale_switch_label()}
      className={cn('inline-flex items-center gap-0.5 rounded-full border border-border/70 p-0.5', className)}
    >
      {appConfig.locales.map((locale) => (
        <button
          key={locale}
          type="button"
          aria-pressed={locale === activeLocale}
          onClick={() => void handleSelect(locale)}
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
