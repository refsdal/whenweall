import { useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'motion/react'
import { m } from '#/lib/i18n'
import { spring, useReducedMotion } from '#/lib/motion'

const BUMP_MS = 500

/**
 * True for a moment after the count goes up, so the pill can acknowledge someone arriving.
 *
 * Only upwards: people leaving a poll is not news, and the number shrinking on its own is quiet
 * enough. The first run just records the starting count.
 */
function useGrew(count: number, durationMs = BUMP_MS): boolean {
  const [grew, setGrew] = useState(false)
  const previous = useRef<number | null>(null)

  useEffect(() => {
    const before = previous.current
    previous.current = count
    if (before === null || count <= before) return

    setGrew(true)
    const timer = setTimeout(() => setGrew(false), durationMs)
    return () => clearTimeout(timer)
  }, [count, durationMs])

  return grew
}

/**
 * "3 viewing" — the quiet signal that a poll is alive. Hidden below two people, because
 * "1 viewing" is just you.
 *
 * The pill crossing that threshold is itself the news, so it springs in and out rather than
 * appearing between one frame and the next, and gives a small nudge whenever the count climbs.
 */
export function PresencePill({ count }: { count: number }) {
  const reduceMotion = useReducedMotion()
  const grew = useGrew(count)

  return (
    <AnimatePresence initial={false}>
      {count >= 2 && (
        <motion.span
          data-testid="presence-pill"
          data-count={count}
          aria-live="polite"
          initial={reduceMotion ? false : { opacity: 0, scale: 0.7 }}
          // The nudge rides on the same spring that brought the pill in: `grew` flips back on a
          // timer and the scale settles home on its own.
          animate={{ opacity: 1, scale: grew && !reduceMotion ? 1.12 : 1 }}
          exit={reduceMotion ? { opacity: 0 } : { opacity: 0, scale: 0.7 }}
          transition={spring}
          className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-1 text-xs font-medium text-muted-foreground"
        >
          <span aria-hidden="true" className="relative flex size-1.5">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-[var(--yes)] opacity-70" />
            <span className="relative inline-flex size-1.5 rounded-full bg-[var(--yes)]" />
          </span>
          {m.poll_presence({ count })}
        </motion.span>
      )}
    </AnimatePresence>
  )
}
