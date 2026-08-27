import { useRouterState } from '@tanstack/react-router'
import { AnimatePresence, motion } from 'motion/react'
import { m } from '#/lib/i18n'

/**
 * Navigations that resolve faster than this never become visible. Most of them do: a flicker of
 * progress on every link would read as the app being slower than it is, not faster.
 *
 * This is a delay on the animation rather than a timer in state — the bar mounts immediately but
 * starts fully transparent, so a route that arrives first unmounts it before it was ever seen.
 */
const SHOW_AFTER_S = 0.18

/**
 * A hairline at the top of the window while a route's loader is running.
 *
 * It creeps towards nine tenths and stops: there is no real percentage to report, and a bar that
 * reached the end while the page was still loading would be a lie. Arrival is the jump to full
 * and the fade, which is the part that reads as "done".
 */
export function RouteProgress() {
  const isLoading = useRouterState({ select: (state) => state.isLoading })

  return (
    <AnimatePresence>
      {isLoading && (
        <motion.div
          role="progressbar"
          aria-label={m.common_loading()}
          className="fixed inset-x-0 top-0 z-50 h-0.5 origin-left bg-primary"
          initial={{ scaleX: 0, opacity: 0 }}
          animate={{
            scaleX: 0.9,
            opacity: 1,
            transition: {
              scaleX: { duration: 2.4, delay: SHOW_AFTER_S, ease: [0.1, 0.9, 0.2, 1] },
              opacity: { duration: 0.12, delay: SHOW_AFTER_S },
            },
          }}
          exit={{ scaleX: 1, opacity: 0, transition: { duration: 0.25, ease: 'easeOut' } }}
        />
      )}
    </AnimatePresence>
  )
}
