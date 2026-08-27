import { useEffect, useSyncExternalStore } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { Moon, Sun, SunMoon } from 'lucide-react'
import { m } from '#/lib/i18n'
import { useReducedMotion } from '#/lib/motion'
import {
  applyTheme,
  getServerThemeSnapshot,
  nextTheme,
  readStoredTheme,
  setStoredTheme,
  subscribeTheme,
  watchSystemTheme,
  type Theme,
} from '#/lib/theme'
import { cn } from '#/lib/utils'

const ICONS: Record<Theme, typeof Sun> = { auto: SunMoon, light: Sun, dark: Moon }
const LABELS: Record<Theme, () => string> = {
  auto: m.theme_auto,
  light: m.theme_light,
  dark: m.theme_dark,
}

/**
 * Cycles the colour theme auto -> light -> dark. The preference lives in `localStorage` and is
 * read through `useSyncExternalStore`, so SSR renders `auto` and the client corrects itself
 * right after hydration — the inline script in `<head>` has already painted the right theme.
 */
export function ThemeToggle({ className }: { className?: string }) {
  const theme = useSyncExternalStore(subscribeTheme, readStoredTheme, getServerThemeSnapshot)
  const reduceMotion = useReducedMotion()

  // Keeps <html> in sync with the stored preference (first paint, other tabs, OS changes).
  useEffect(() => {
    applyTheme(theme)
    if (theme !== 'auto') return
    return watchSystemTheme(() => applyTheme('auto'))
  }, [theme])

  const Icon = ICONS[theme]

  return (
    <button
      type="button"
      onClick={() => setStoredTheme(nextTheme(theme))}
      data-theme={theme}
      aria-label={m.theme_switch({ mode: LABELS[theme]() })}
      title={`${m.theme_label()}: ${LABELS[theme]()}`}
      className={cn(
        'focus-ring inline-flex size-9 items-center justify-center rounded-full border border-border/70 text-foreground/70',
        'transition-colors hover:border-border hover:bg-secondary hover:text-foreground',
        className,
      )}
    >
      {/* The incoming icon already popped in; the outgoing one used to just disappear under it.
          `mode="wait"` lets it rotate away first, and it leaves in the opposite direction to the
          one the next icon arrives from, so the three read as a wheel turning one way. */}
      <AnimatePresence mode="wait" initial={false}>
        <motion.span
          key={theme}
          aria-hidden="true"
          initial={reduceMotion ? false : { opacity: 0, scale: 0.7, rotate: -25 }}
          animate={{ opacity: 1, scale: 1, rotate: 0 }}
          exit={reduceMotion ? { opacity: 0 } : { opacity: 0, scale: 0.7, rotate: 25 }}
          transition={{ duration: 0.14, ease: 'easeOut' }}
          className="flex items-center justify-center"
        >
          <Icon className="size-4" aria-hidden="true" />
        </motion.span>
      </AnimatePresence>
    </button>
  )
}
