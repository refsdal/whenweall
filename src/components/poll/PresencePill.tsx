import { m } from '#/lib/i18n'

/**
 * "3 viewing" — the quiet signal that a poll is alive. Hidden below two people, because
 * "1 viewing" is just you.
 */
export function PresencePill({ count }: { count: number }) {
  if (count < 2) return null

  return (
    <span
      data-testid="presence-pill"
      data-count={count}
      aria-live="polite"
      className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-1 text-xs font-medium text-muted-foreground"
    >
      <span aria-hidden="true" className="relative flex size-1.5">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-[var(--yes)] opacity-70" />
        <span className="relative inline-flex size-1.5 rounded-full bg-[var(--yes)]" />
      </span>
      {m.poll_presence({ count })}
    </span>
  )
}
