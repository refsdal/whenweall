import { Link } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { buttonVariants } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

/**
 * Three empty grid cells, waiting for votes — a quiet nod to the vote grid without trying to be
 * a literal screenshot of it.
 */
function ThreeCellsIllustration() {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 120 72"
      className="h-16 w-auto text-muted-foreground/70"
      fill="none"
    >
      {[0, 1, 2].map((i) => (
        <rect
          key={i}
          x={4 + i * 40}
          y={4}
          width={32}
          height={64}
          rx={8}
          className="fill-secondary"
          stroke="currentColor"
          strokeOpacity={0.25}
        />
      ))}
      <circle cx={20} cy={20} r={5} className="fill-yes-soft" stroke="var(--yes)" />
      <circle cx={60} cy={36} r={5} className="fill-ifneedbe-soft" stroke="var(--ifneedbe)" />
      <circle
        cx={100}
        cy={52}
        r={5}
        className="fill-muted"
        stroke="currentColor"
        strokeOpacity={0.3}
      />
    </svg>
  )
}

export function EmptyState() {
  return (
    <div className="flex flex-col items-center gap-4 rounded-xl border border-dashed border-border px-6 py-16 text-center">
      <ThreeCellsIllustration />
      <div className="flex flex-col gap-1">
        <p className="font-medium">{m.dashboard_empty_title()}</p>
        <p className="max-w-xs text-sm text-balance text-muted-foreground">
          {m.dashboard_empty_body()}
        </p>
      </div>
      <Link to="/new" className={cn(buttonVariants(), 'gap-1.5')}>
        <Plus aria-hidden="true" />
        {m.nav_new_poll()}
      </Link>
    </div>
  )
}
