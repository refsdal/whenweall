export const THEMES = ['auto', 'light', 'dark'] as const

export type Theme = (typeof THEMES)[number]
export type ResolvedTheme = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'theme'

const DARK_QUERY = '(prefers-color-scheme: dark)'

function isTheme(value: unknown): value is Theme {
  return typeof value === 'string' && (THEMES as readonly string[]).includes(value)
}

/** Reads the stored preference; falls back to `auto` when unset or unreadable. */
export function readStoredTheme(): Theme {
  if (typeof window === 'undefined') return 'auto'
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY)
    return isTheme(stored) ? stored : 'auto'
  } catch {
    // Private mode / blocked storage: fall back to following the system.
    return 'auto'
  }
}

/** Resolves `auto` against the OS preference. */
export function resolveTheme(theme: Theme): ResolvedTheme {
  if (theme !== 'auto') return theme
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'light'
  return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

/** The next value in the auto -> light -> dark -> auto cycle. */
export function nextTheme(theme: Theme): Theme {
  const index = THEMES.indexOf(theme)
  return THEMES[(index + 1) % THEMES.length] ?? 'auto'
}

/**
 * Persists the preference and applies the resolved theme to `<html>`. The explicit class is
 * what Tailwind's `dark:` variant keys off, so it is set even when the preference is `auto`.
 */
export function applyTheme(theme: Theme): ResolvedTheme {
  const resolved = resolveTheme(theme)
  if (typeof document !== 'undefined') {
    const root = document.documentElement
    root.classList.toggle('dark', resolved === 'dark')
    root.classList.toggle('light', resolved === 'light')
    root.style.colorScheme = resolved
  }
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme)
  } catch {
    // Ignore: the theme still applies for this page view.
  }
  return resolved
}

const listeners = new Set<() => void>()

/**
 * Subscribes to preference changes — both from this tab (via `setStoredTheme`) and from
 * other tabs (via the `storage` event). Shaped for `useSyncExternalStore`.
 */
export function subscribeTheme(onChange: () => void): () => void {
  listeners.add(onChange)
  if (typeof window !== 'undefined') window.addEventListener('storage', onChange)
  return () => {
    listeners.delete(onChange)
    if (typeof window !== 'undefined') window.removeEventListener('storage', onChange)
  }
}

/** The snapshot `useSyncExternalStore` renders during SSR and hydration. */
export function getServerThemeSnapshot(): Theme {
  return 'auto'
}

/** Stores and applies a preference, then notifies subscribers. */
export function setStoredTheme(theme: Theme): void {
  applyTheme(theme)
  for (const listener of listeners) listener()
}

/** Subscribes to OS theme changes. Returns an unsubscribe function. */
export function watchSystemTheme(onChange: () => void): () => void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return () => {}
  const query = window.matchMedia(DARK_QUERY)
  query.addEventListener('change', onChange)
  return () => query.removeEventListener('change', onChange)
}

/**
 * Runs synchronously in `<head>` before first paint so the page never flashes the wrong
 * theme. Mirrors `applyTheme` — keep the two in sync.
 */
export const themeInitScript = `(function(){try{var t=localStorage.getItem('${THEME_STORAGE_KEY}');if(t!=='light'&&t!=='dark'&&t!=='auto'){t='auto'}var d=t==='dark'||(t==='auto'&&window.matchMedia('${DARK_QUERY}').matches);var e=document.documentElement;e.classList.toggle('dark',d);e.classList.toggle('light',!d);e.style.colorScheme=d?'dark':'light'}catch(_){}})()`
