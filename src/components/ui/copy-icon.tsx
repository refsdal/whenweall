import { AnimatePresence, motion } from 'motion/react'
import { Check, Copy } from 'lucide-react'
import { useReducedMotion } from '#/lib/motion'

/**
 * The clipboard icon and the tick it turns into, swapped the same way a vote cell swaps its mark:
 * the outgoing glyph shrinks away while the incoming one pops in. A hard cut here reads as a
 * re-render; the pop reads as the click having landed.
 */
export function CopyIcon({ copied }: { copied: boolean }) {
  const reduceMotion = useReducedMotion()
  const Icon = copied ? Check : Copy

  return (
    <AnimatePresence initial={false} mode="wait">
      <motion.span
        key={copied ? 'copied' : 'idle'}
        aria-hidden="true"
        initial={reduceMotion ? false : { opacity: 0, scale: 0.5 }}
        animate={{ opacity: 1, scale: 1 }}
        exit={reduceMotion ? { opacity: 1 } : { opacity: 0, scale: 0.5 }}
        transition={{ duration: 0.12, ease: 'easeOut' }}
        className="flex items-center justify-center"
      >
        <Icon aria-hidden="true" />
      </motion.span>
    </AnimatePresence>
  )
}
