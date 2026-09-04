import type { ReactNode } from 'react'
import { cn } from '#/lib/utils'

/** What a failed attempt looks like in state: the message, and which attempt produced it. */
export type FormFailure = { message: string; attempt: number }

/**
 * Records a failure, counting attempts.
 *
 * The count is what makes a repeated mistake visible. Pressing submit twice on an empty field
 * produces the same sentence both times, and a component whose props did not change does not
 * re-render — so without something that moves, the second press would look like nothing happened
 * at all. Callers key `FormError` on it.
 */
export function nextFailure(current: FormFailure | null, message: string): FormFailure {
  return { message, attempt: (current?.attempt ?? 0) + 1 }
}

/**
 * An inline validation message that shakes as it arrives.
 *
 * Inline rather than a toast in both places this is used: the field that needs fixing is right
 * there, and a dialog steals focus from the toast region anyway.
 */
export function FormError({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <p role="alert" className={cn('shake-once text-sm text-destructive', className)}>
      {children}
    </p>
  )
}
