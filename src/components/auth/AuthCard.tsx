import type { ReactNode } from 'react'
import { motion } from 'motion/react'
import { Card, CardContent, CardHeader } from '#/components/ui/card'
import { useReducedMotion } from '#/lib/motion'

/**
 * Shared shell for every auth page: a centered card with a display-font title, an optional
 * subtitle, the flow's form/content, and an optional footer for cross-links (e.g. "Sign up"
 * under the login form). Fades/slides in on mount — a small, restrained flourish that
 * `useReducedMotion` (via `MotionConfig reducedMotion="user"` in the root layout) turns off for
 * anyone who's asked for less motion.
 */
export function AuthCard({
  title,
  subtitle,
  children,
  footer,
}: {
  title: string
  subtitle?: string
  children: ReactNode
  footer?: ReactNode
}) {
  const reduceMotion = useReducedMotion()

  return (
    <div className="mx-auto flex w-full max-w-md flex-col px-5 py-12 sm:py-20">
      <motion.div
        initial={reduceMotion ? false : { opacity: 0, y: 14 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.4, ease: [0.22, 1, 0.36, 1] }}
      >
        <Card>
          <CardHeader className="gap-1.5 text-center">
            <h1 className="display text-2xl">{title}</h1>
            {subtitle ? <p className="text-sm text-muted-foreground">{subtitle}</p> : null}
          </CardHeader>
          <CardContent className="flex flex-col gap-5">{children}</CardContent>
        </Card>
        {footer ? (
          <div className="mt-6 flex flex-col items-center gap-2 text-center text-sm text-muted-foreground">
            {footer}
          </div>
        ) : null}
      </motion.div>
    </div>
  )
}
