import { useEffect, useRef, useState } from 'react'
import { useReducedMotion } from 'motion/react'
import { totalResponses, type UsageStats as Stats } from '#/do/stats-protocol'
import { useLiveStats } from '#/lib/use-live-stats'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

const COUNT_UP_MS = 600

/**
 * Counts from the previous value to the next one so a number that changes while someone is reading
 * draws the eye. Snaps instantly when the viewer prefers reduced motion, and on the very first
 * render — the server-rendered number must not animate up from zero on hydration.
 */
function useCountUp(value: number, animate: boolean): number {
  const [display, setDisplay] = useState(value)
  const previous = useRef(value)
  const firstRender = useRef(true)

  useEffect(() => {
    const from = previous.current
    previous.current = value

    if (firstRender.current) {
      firstRender.current = false
      setDisplay(value)
      return
    }
    if (!animate || from === value) {
      setDisplay(value)
      return
    }

    let raf = 0
    const started = performance.now()
    const tick = (now: number) => {
      const progress = Math.min(1, (now - started) / COUNT_UP_MS)
      // easeOutQuad: fast to start, settles gently on the final number.
      const eased = 1 - (1 - progress) * (1 - progress)
      setDisplay(Math.round(from + (value - from) * eased))
      if (progress < 1) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [value, animate])

  return display
}

function Figure({ value, label, animate }: { value: number; label: string; animate: boolean }) {
  const display = useCountUp(value, animate)
  return (
    <div className="flex flex-col gap-1">
      <span className="display text-4xl tabular-nums sm:text-5xl">{display.toLocaleString()}</span>
      <span className="text-sm text-muted-foreground">{label}</span>
    </div>
  )
}

/**
 * Live usage counters. The numbers arrive server-rendered from the route loader, so this is
 * correct with JavaScript disabled; the socket only opens once the section scrolls into view, so a
 * visitor who never reaches it never opens one.
 *
 * Deliberately shown at any size — see the spec's §6. A small number here reads as "early", which
 * is the message, not something to hide behind a threshold.
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

  return (
    <section ref={ref} className={cn('flex flex-col gap-6', className)}>
      <p className="text-sm font-medium tracking-[0.14em] text-[var(--primary-ink)] uppercase">
        {m.landing_stats_eyebrow()}
      </p>

      <div className="flex flex-wrap items-end gap-x-12 gap-y-6">
        <Figure value={stats.pollsCreated} label={m.landing_stats_polls()} animate={animate} />
        <Figure
          value={totalResponses(stats)}
          label={m.landing_stats_responses()}
          animate={animate}
        />
      </div>

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
