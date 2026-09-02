import { getLocale } from '#/paraglide/runtime'
import { appConfig, type AppLocale } from '#/app.config'

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
 * `whenweall_locale` cookie / document.
 */
export function localeFromRequest(): AppLocale {
  return getLocale()
}

/** Narrows an arbitrary string to a locale this app actually ships messages for. */
export function isAppLocale(value: string): value is AppLocale {
  return (appConfig.locales as readonly string[]).includes(value)
}

/**
 * Builds the `options` argument Paraglide message functions take, from a locale that is only
 * known as a `string` at runtime (a database column, an email recipient's preference, ...).
 * Unknown locales fall back to the base locale rather than rendering an untranslated key.
 */
export function asLocaleOptions(locale: string): { locale: AppLocale } {
  return { locale: isAppLocale(locale) ? locale : appConfig.defaultLocale }
}
