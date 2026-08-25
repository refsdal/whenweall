import { Check, Minus, X } from 'lucide-react'
import { intlLocale, getLocale, m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

type Answer = 'yes' | 'ifneedbe' | 'no'

const ANSWER_STYLES: Record<Answer, { bg: string; ink: string; Icon: typeof Check }> = {
  yes: { bg: 'var(--yes-soft)', ink: 'var(--yes-ink)', Icon: Check },
  ifneedbe: { bg: 'var(--ifneedbe-soft)', ink: 'var(--ifneedbe-ink)', Icon: Minus },
  no: { bg: 'var(--no-soft)', ink: 'var(--no-ink)', Icon: X },
}

// Fixed dates so server and client render the same labels; formatted per locale.
const OPTION_DATES = [new Date(2027, 4, 11), new Date(2027, 4, 12), new Date(2027, 4, 13)]
const BEST_COLUMN = 1

const PEOPLE: { name: string; answers: [Answer, Answer, Answer] }[] = [
  { name: 'Ada', answers: ['no', 'yes', 'ifneedbe'] },
  { name: 'Iben', answers: ['ifneedbe', 'yes', 'no'] },
  { name: 'Jonas', answers: ['yes', 'yes', 'yes'] },
  { name: 'Mira', answers: ['no', 'yes', 'ifneedbe'] },
]

/**
 * Decorative illustration of a poll filling up: cells pop into their answer colour one after
 * another, then the winning column is crowned. Pure CSS keyframes (see `styles.css`), so it
 * costs nothing on the LCP path and stops entirely under `prefers-reduced-motion`.
 */
export function VoteGridMock({ className }: { className?: string }) {
  const locale = intlLocale(getLocale())
  const weekday = new Intl.DateTimeFormat(locale, { weekday: 'short' })
  const day = new Intl.DateTimeFormat(locale, { day: 'numeric', month: 'short' })

  return (
    <div className={cn('relative', className)} role="img" aria-label={m.landing_mock_caption()}>
      <div
        aria-hidden="true"
        className="absolute -inset-8 -z-10 rounded-[3rem] bg-[radial-gradient(60%_60%_at_50%_40%,var(--accent-soft),transparent_70%)] blur-2xl"
      />

      <div
        aria-hidden="true"
        className="surface w-full p-4 sm:p-5 md:rotate-[-1.25deg] md:transition-transform md:duration-500 md:hover:rotate-0"
      >
        <div className="mb-4 flex items-start justify-between gap-3">
          <div>
            <p className="display text-base">{m.landing_mock_title()}</p>
            <p className="text-xs text-muted-foreground">
              {m.landing_mock_meta({
                options: String(OPTION_DATES.length),
                people: String(PEOPLE.length),
              })}
            </p>
          </div>
          <span className="inline-flex items-center gap-1.5 rounded-full bg-secondary px-2.5 py-1 text-[0.6875rem] font-medium text-muted-foreground">
            <span className="relative flex size-1.5">
              <span className="absolute inline-flex size-full animate-ping rounded-full bg-[var(--yes)] opacity-70" />
              <span className="relative inline-flex size-1.5 rounded-full bg-[var(--yes)]" />
            </span>
            {m.landing_why_live()}
          </span>
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)_repeat(3,2.5rem)] gap-1.5 sm:grid-cols-[minmax(0,1fr)_repeat(3,3.25rem)] sm:gap-2">
          {/* A reserved row for the crown, so the badge can never collide with the header. */}
          <div className="h-5" />
          {OPTION_DATES.map((date, column) => (
            <div key={`crown-${date.toISOString()}`} className="flex h-5 items-end justify-center">
              {column === BEST_COLUMN && (
                <span
                  data-mock-best
                  className="animate-best-pulse rounded-full bg-[var(--primary-strong)] px-2 py-0.5 text-[0.5625rem] font-semibold tracking-wide whitespace-nowrap text-[var(--primary-foreground)] uppercase"
                >
                  {m.landing_mock_best()}
                </span>
              )}
            </div>
          ))}

          <div />
          {OPTION_DATES.map((date, column) => (
            <div
              key={date.toISOString()}
              className={cn(
                'rounded-lg px-1 py-1.5 text-center',
                column === BEST_COLUMN && 'bg-accent-soft ring-1 ring-[var(--primary)]/35',
              )}
            >
              <span className="block text-[0.625rem] tracking-wide text-muted-foreground uppercase">
                {weekday.format(date)}
              </span>
              <span className="block text-xs font-semibold">{day.format(date)}</span>
            </div>
          ))}

          {PEOPLE.map((person, row) => (
            <Row key={person.name} person={person} row={row} />
          ))}
        </div>
      </div>
    </div>
  )
}

function Row({ person, row }: { person: (typeof PEOPLE)[number]; row: number }) {
  return (
    <>
      <div className="flex items-center gap-2 pr-2 text-sm">
        <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-secondary text-[0.625rem] font-semibold text-secondary-foreground">
          {person.name.slice(0, 1)}
        </span>
        <span className="truncate text-xs text-muted-foreground sm:text-sm">{person.name}</span>
      </div>
      {person.answers.map((answer, column) => {
        const { bg, ink, Icon } = ANSWER_STYLES[answer]
        const delay = `${(row * 3 + column) * 0.26}s`
        return (
          <div
            key={column}
            data-mock-cell
            style={
              {
                '--cell-bg': bg,
                color: ink,
                animationDelay: delay,
              } as React.CSSProperties
            }
            className="animate-cell-in flex h-9 items-center justify-center rounded-lg sm:h-10"
          >
            <Icon
              data-mock-mark
              className="size-4 animate-[whenweall-cell-mark_9s_ease-in-out_infinite]"
              style={{ animationDelay: delay }}
            />
          </div>
        )
      })}
    </>
  )
}
