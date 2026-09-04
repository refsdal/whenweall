import { createFileRoute, Link, useRouter } from '@tanstack/react-router'
import { AnimatePresence, motion } from 'motion/react'
import { ArrowRight, CalendarClock } from 'lucide-react'
import { toast } from 'sonner'
import { appConfig } from '#/app.config'
import { EmptyState } from '#/components/dashboard/EmptyState'
import { PollCard } from '#/components/dashboard/PollCard'
import { m } from '#/lib/i18n'
import { requireVerifiedSession } from '#/lib/session-guard'
import { cn } from '#/lib/utils'
import { staggerContainer, staggerItem, useReducedMotion } from '#/lib/motion'
import { buttonVariants } from '#/components/ui/button'
import { deletePoll, duplicatePoll, listMyPolls } from '#/api/polls'

export const Route = createFileRoute('/dashboard')({
  beforeLoad: ({ context }) => requireVerifiedSession(context, '/dashboard'),
  loader: () => listMyPolls(),
  head: () => ({
    meta: [{ title: `${m.dashboard_title()} — ${appConfig.name}` }],
  }),
  component: DashboardPage,
})

function DashboardPage() {
  const polls = Route.useLoaderData()
  const reduceMotion = useReducedMotion()
  const router = useRouter()
  const navigate = Route.useNavigate()

  async function handleDuplicate(pollId: string) {
    try {
      const { id } = await duplicatePoll(pollId)
      toast.success(m.poll_duplicated())
      await navigate({ to: '/p/$id', params: { id } })
    } catch {
      toast.error(m.poll_error_generic())
    }
  }

  async function handleDelete(pollId: string) {
    try {
      await deletePoll(pollId)
      toast.success(m.poll_deleted())
      await router.invalidate()
    } catch {
      toast.error(m.poll_error_generic())
    }
  }

  return (
    <div
      data-testid="dashboard"
      className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-5 py-10 sm:py-14"
    >
      <header className="flex flex-col gap-1">
        <h1 className="display text-3xl">{m.dashboard_title()}</h1>
        <p className="text-sm text-muted-foreground">{m.dashboard_subtitle()}</p>
      </header>

      {polls.length === 0 ? (
        <EmptyState />
      ) : (
        <motion.ul
          initial="initial"
          animate="animate"
          variants={staggerContainer}
          className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3"
        >
          {/* `popLayout` takes a deleted card out of the flow before it has finished shrinking, so
              the cards after it start closing the gap straight away rather than waiting. */}
          <AnimatePresence initial={false} mode="popLayout">
            {polls.map((poll) => (
              <motion.li
                key={poll.id}
                variants={staggerItem}
                layout={!reduceMotion}
                exit={
                  reduceMotion
                    ? { opacity: 0 }
                    : { opacity: 0, scale: 0.94, transition: { duration: 0.18, ease: 'easeOut' } }
                }
              >
                <PollCard
                  poll={poll}
                  onDuplicate={() => handleDuplicate(poll.id)}
                  onDelete={() => handleDelete(poll.id)}
                />
              </motion.li>
            ))}
          </AnimatePresence>
        </motion.ul>
      )}

      <BookingPagesCard />
    </div>
  )
}

/** The doorway to v3's booking pages: a quiet card under the polls rather than a second nav item
 * competing with "New poll". */
function BookingPagesCard() {
  return (
    <section className="surface mt-2 flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:gap-4">
      <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-accent-soft text-accent-foreground">
        <CalendarClock aria-hidden="true" className="size-4" />
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="font-medium">{m.dashboard_bookings_title()}</p>
        <p className="text-sm text-pretty text-muted-foreground">{m.dashboard_bookings_body()}</p>
      </div>
      <Link
        to="/bookings"
        className={cn(buttonVariants({ variant: 'outline', size: 'sm' }), 'shrink-0 gap-1.5')}
      >
        {m.dashboard_bookings_cta()}
        <ArrowRight aria-hidden="true" />
      </Link>
    </section>
  )
}
