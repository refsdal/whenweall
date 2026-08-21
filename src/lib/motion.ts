export { useReducedMotion } from 'motion/react'

/** The one spring the whole product animates with: quick, barely overshooting, never floaty. */
export const spring = { type: 'spring', stiffness: 500, damping: 30, mass: 0.6 } as const

/** Press feedback for buttons and vote cells. */
export const tapScale = { whileTap: { scale: 0.94 }, transition: spring } as const

/** Enter/exit for items that appear in a list (options, participants, comments). */
export const listItem = {
  initial: { opacity: 0, y: 6 },
  animate: { opacity: 1, y: 0 },
  exit: { opacity: 0, y: -6 },
  transition: spring,
} as const

/** Section reveal used on the landing page; pair with `staggerChildren` on the parent. */
export const fadeUp = {
  initial: { opacity: 0, y: 14 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.5, ease: [0.22, 1, 0.36, 1] },
} as const

/** Parent variants that stagger `fadeUp` children in. */
export const staggerContainer = {
  initial: {},
  animate: { transition: { staggerChildren: 0.12, delayChildren: 0.08 } },
} as const

/** Child variants matching `staggerContainer`. */
export const staggerItem = {
  initial: { opacity: 0, y: 14 },
  animate: { opacity: 1, y: 0, transition: { duration: 0.5, ease: [0.22, 1, 0.36, 1] } },
} as const
