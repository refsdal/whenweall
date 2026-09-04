import { Link } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

/**
 * The whenweall mark: three option columns with the winning one filled in ember coral —
 * the same shape language as the vote grid.
 */
export function LogoMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      aria-hidden="true"
      className={cn('size-6', className)}
      fill="none"
      role="presentation"
    >
      <rect x="2.5" y="9" width="5" height="9" rx="2" className="fill-current opacity-25" />
      <rect x="16.5" y="9" width="5" height="9" rx="2" className="fill-current opacity-25" />
      <rect
        x="9.5"
        y="6"
        width="5"
        height="12"
        rx="2.2"
        className="origin-bottom fill-[var(--primary)] transition-transform duration-300 ease-out group-hover:scale-y-110"
        style={{ transformBox: 'fill-box' }}
      />
      <circle cx="12" cy="2.9" r="1.6" className="fill-[var(--primary)]" />
    </svg>
  )
}

export function Logo({ className }: { className?: string }) {
  return (
    <Link
      to="/"
      aria-label={m.nav_home()}
      className={cn(
        'focus-ring group inline-flex items-center gap-2 rounded-full pr-2 text-foreground',
        className,
      )}
    >
      <LogoMark />
      <span className="display text-xl leading-none">
        {appConfig.name}
        <span className="text-[var(--primary)]">.</span>
      </span>
    </Link>
  )
}
