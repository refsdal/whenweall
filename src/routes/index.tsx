import { Link, createFileRoute } from '@tanstack/react-router'
import { motion } from 'motion/react'
import { ArrowRight, CalendarPlus, Check, Link2, Sparkles, Users } from 'lucide-react'
import { createServerFn } from '@tanstack/react-start'
import { appConfig } from '#/app.config'
import type { UsageStats } from '#/do/stats-protocol'
import { UsageStatsSection } from '#/components/landing/UsageStats'
import { VoteGridMock } from '#/components/landing/VoteGridMock'
import { DecideTogether } from '#/components/landing/steps/DecideTogether'
import { ProposeTimes } from '#/components/landing/steps/ProposeTimes'
import { ShareLink } from '#/components/landing/steps/ShareLink'
import { buttonVariants } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { staggerContainer, staggerItem } from '#/lib/motion'
import { cn } from '#/lib/utils'

/** Server-rendered so the landing page shows real numbers on first paint — no zero-flash, no
 * layout shift, and correct with JavaScript disabled. */
const getUsageStats = createServerFn({ method: 'GET' }).handler(async () => {
  const { readUsageStats } = await import('#/server/stats/stats-client')
  return readUsageStats()
})

export const Route = createFileRoute('/')({
  loader: async (): Promise<{ stats: UsageStats }> => ({ stats: await getUsageStats() }),
  head: () => ({
    meta: [
      { title: `${appConfig.name} — ${appConfig.tagline}` },
      { name: 'description', content: appConfig.description },
    ],
  }),
  component: LandingPage,
})

const CONTAINER = 'mx-auto w-full max-w-6xl px-5 sm:px-8'

function LandingPage() {
  const { stats } = Route.useLoaderData()
  return (
    <>
      <Hero />
      <HowItWorks />
      <UsageStatsSection initial={stats} className={cn(CONTAINER, 'pb-16 sm:pb-24')} />
      <Outro />
    </>
  )
}

function Hero() {
  return (
    <section className="relative overflow-hidden">
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 -top-40 h-[32rem] bg-[radial-gradient(50%_50%_at_50%_50%,var(--accent-soft),transparent_75%)] opacity-70"
      />

      <div
        className={cn(
          CONTAINER,
          'relative grid items-center gap-14 py-16 sm:py-24 lg:grid-cols-[1.05fr_0.95fr] lg:gap-16',
        )}
      >
        <div className="flex flex-col items-start">
          <span className="inline-flex items-center gap-1.5 rounded-full border border-border/70 bg-card/70 px-3 py-1 text-xs font-medium text-muted-foreground">
            <Sparkles className="size-3.5 text-[var(--primary)]" aria-hidden="true" />
            {m.landing_badge()}
          </span>

          <h1 className="display mt-6 text-4xl leading-[1.05] text-balance sm:text-5xl lg:text-6xl">
            {m.landing_title()}
          </h1>

          <p className="mt-5 max-w-xl text-lg leading-relaxed text-muted-foreground sm:text-xl">
            {m.landing_sub()}
          </p>

          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Link to="/new" className={cn(buttonVariants({ size: 'lg' }), 'group')}>
              {m.landing_cta()}
              <ArrowRight
                className="transition-transform duration-200 group-hover:translate-x-0.5"
                aria-hidden="true"
              />
            </Link>
            <a href="#how" className={cn(buttonVariants({ variant: 'outline', size: 'lg' }))}>
              {m.landing_cta_secondary()}
            </a>
          </div>

          <ul className="mt-8 flex flex-wrap gap-x-6 gap-y-2 text-sm text-muted-foreground">
            {[m.landing_why_free(), m.landing_why_no_account(), m.landing_why_live()].map(
              (label) => (
                <li key={label} className="inline-flex items-center gap-1.5">
                  <Check className="size-4 text-[var(--yes)]" aria-hidden="true" />
                  {label}
                </li>
              ),
            )}
          </ul>
        </div>

        <VoteGridMock className="lg:justify-self-end" />
      </div>
    </section>
  )
}

const STEPS = [
  {
    Icon: CalendarPlus,
    Visual: ProposeTimes,
    title: m.landing_step_1_title,
    body: m.landing_step_1_body,
  },
  { Icon: Link2, Visual: ShareLink, title: m.landing_step_2_title, body: m.landing_step_2_body },
  {
    Icon: Users,
    Visual: DecideTogether,
    title: m.landing_step_3_title,
    body: m.landing_step_3_body,
  },
]

function HowItWorks() {
  return (
    <section id="how" className={cn(CONTAINER, 'scroll-mt-24 py-16 sm:py-24')}>
      <p className="text-sm font-medium tracking-[0.14em] text-[var(--primary-ink)] uppercase">
        {m.landing_how_eyebrow()}
      </p>
      <h2 className="display mt-3 max-w-lg text-3xl leading-[1.1] text-balance sm:text-4xl">
        {m.landing_how_title()}
      </h2>

      <motion.ol
        variants={staggerContainer}
        initial="initial"
        whileInView="animate"
        viewport={{ once: true, amount: 0.25 }}
        className="mt-10 grid gap-5 sm:grid-cols-3"
      >
        {STEPS.map((step, index) => (
          <motion.li
            key={step.title()}
            variants={staggerItem}
            className="surface group relative flex flex-col gap-3 p-6 transition-shadow duration-300 hover:shadow-[0_1px_2px_-1px_hsl(var(--shadow-color)/0.1),0_18px_40px_-24px_hsl(var(--shadow-color)/0.35)]"
          >
            <step.Visual />
            <span className="inline-flex size-10 items-center justify-center rounded-full bg-accent-soft text-accent-foreground transition-transform duration-300 group-hover:-rotate-6">
              <step.Icon className="size-5" aria-hidden="true" />
            </span>
            <h3 className="display text-lg">
              <span aria-hidden="true" className="mr-2 text-muted-foreground tabular-nums">
                {index + 1}
              </span>
              {step.title()}
            </h3>
            <p className="text-sm leading-relaxed text-muted-foreground">{step.body()}</p>
          </motion.li>
        ))}
      </motion.ol>
    </section>
  )
}

function Outro() {
  return (
    <section className={cn(CONTAINER, 'pb-8')}>
      <div className="surface relative overflow-hidden px-6 py-12 text-center sm:px-12 sm:py-16">
        <div
          aria-hidden="true"
          className="pointer-events-none absolute inset-x-0 -bottom-24 h-56 bg-[radial-gradient(50%_60%_at_50%_50%,var(--accent-soft),transparent_70%)]"
        />
        <div className="relative">
          <h2 className="display text-3xl leading-[1.1] text-balance sm:text-4xl">
            {m.landing_outro_title()}
          </h2>
          <p className="mx-auto mt-3 max-w-md text-muted-foreground">{m.landing_outro_body()}</p>
          <Link to="/new" className={cn(buttonVariants({ size: 'lg' }), 'group mt-7')}>
            {m.landing_cta()}
            <ArrowRight
              className="transition-transform duration-200 group-hover:translate-x-0.5"
              aria-hidden="true"
            />
          </Link>
        </div>
      </div>
    </section>
  )
}
