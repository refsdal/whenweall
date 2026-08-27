import { useEffect, useRef, useState } from 'react'
import { useReducedMotion } from 'motion/react'
import { totalResponses, type UsageStats as Stats } from '#/do/stats-protocol'
import { useLiveStats } from '#/lib/use-live-stats'
import { m } from '#/lib/i18n'
import { useCountUp } from '#/lib/use-count-up'
import { cn } from '#/lib/utils'

/**
 * A character that cannot occur in translated copy, used to mark where the highlighted number
 * goes. Paraglide messages return plain strings, so the number cannot be JSX inside the message —
 * interpolating a sentinel and splitting on it keeps the sentence as **one** translatable unit and
 * lets each locale place the number wherever its grammar wants, which a prefix/suffix pair could
 * not do.
 */
const SLOT = '\u0000'

/**
 * Splits a sentence around the marker so the number can be rendered as its own element. Returns
 * the literal parts; the number belongs between each adjacent pair. A translation that lost its
 * placeholder yields a single part and renders as plain prose rather than throwing.
 */
export function splitAroundSlot(sentence: string): string[] {
  return sentence.split(SLOT)
}

/** The highlighted, live-updating number inside the sentence. */
function InlineCount({ value, animate }: { value: number; animate: boolean }) {
  const display = useCountUp(value, animate)
  return (
    <span className="font-semibold text-[var(--primary-ink)] tabular-nums">
      {display.toLocaleString()}
    </span>
  )
}

/**
 * Live usage counters, framed as a sentence rather than a dashboard.
 *
 * The headline is the number of *decided* polls — the outcome, not the attempt. It is structurally
 * the smallest of the counters (abandoned polls, deadline closes with no winner, and sign-up
 * sheets which cannot be finalized at all), so putting it in a sentence rather than beside a
 * larger figure avoids inviting a comparison that reads as a conversion rate.
 *
 * Numbers arrive server-rendered from the route loader, so this is correct with JavaScript
 * disabled; the socket only opens once the section scrolls into view.
 */
export function UsageStatsSection({ initial, className }: { initial: Stats; className?: string }) {
  const reduced = useReducedMotion()
  const [inView, setInView] = useState(false)
  const ref = useRef<HTMLElement | null>(null)

  useEffect(() => {
    const node = ref.current
    if (!node || typeof IntersectionObserver === 'undefined') return

    const observer = new IntersectionObserver(
      ([entry]) => setInView(entry?.isIntersecting ?? false),
      { rootMargin: '96px' },
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [])

  const stats = useLiveStats(initial, inView)
  const animate = !reduced

  // `{count}` is replaced by the sentinel, then the sentence is split around it. `parts` is always
  // at least one string, so a translation that somehow lost the placeholder still renders as plain
  // prose rather than throwing.
  const parts = splitAroundSlot(m.landing_stats_sentence({ count: SLOT }))

  return (
    <section ref={ref} className={cn('flex flex-col gap-5', className)}>
      <p className="display max-w-2xl text-3xl leading-[1.15] text-balance sm:text-4xl">
        {parts.map((part, index) => (
          <span key={index}>
            {part}
            {index < parts.length - 1 ? (
              <InlineCount value={stats.pollsFinalized} animate={animate} />
            ) : null}
          </span>
        ))}
      </p>

      <p className="text-sm text-muted-foreground">
        {m.landing_stats_supporting({
          polls: stats.pollsCreated.toLocaleString(),
          responses: totalResponses(stats).toLocaleString(),
        })}
      </p>

      <ul className="flex flex-wrap gap-x-6 gap-y-2 text-sm text-muted-foreground">
        <li className="inline-flex items-center gap-1.5">
          <span className="size-2 rounded-full bg-[var(--yes)]" aria-hidden="true" />
          <span className="tabular-nums">{stats.responsesYes.toLocaleString()}</span>
          {m.landing_stats_yes()}
        </li>
        <li className="inline-flex items-center gap-1.5">
          <span className="size-2 rounded-full bg-[var(--ifneedbe)]" aria-hidden="true" />
          <span className="tabular-nums">{stats.responsesIfNeedBe.toLocaleString()}</span>
          {m.landing_stats_ifneedbe()}
        </li>
        <li className="inline-flex items-center gap-1.5">
          <span className="size-2 rounded-full bg-[var(--no)]" aria-hidden="true" />
          <span className="tabular-nums">{stats.responsesNo.toLocaleString()}</span>
          {m.landing_stats_no()}
        </li>
      </ul>
    </section>
  )
}
