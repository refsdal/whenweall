import { getLocale } from '#/paraglide/runtime'
import type { AppLocale } from '#/app.config'

export { m } from '#/paraglide/messages'
export { baseLocale, getLocale, locales, setLocale } from '#/paraglide/runtime'

/**
 * Maps an app locale to the BCP-47 tag used for `Intl` formatting (dates, numbers, etc).
 */
export function intlLocale(locale: string): string {
  if (locale === 'nb') return 'nb-NO'
  if (locale === 'en') return 'en-GB'
  return locale
}

/**
 * Reads the locale for the current request. Must be called within the
 * `paraglideMiddleware` async context on the server (e.g. during SSR or a
 * server function), or on the client where Paraglide resolves it from the
 * `samla_locale` cookie / document.
 */
export function localeFromRequest(): AppLocale {
  return getLocale()
}
