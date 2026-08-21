import { createFileRoute, redirect, useRouter } from '@tanstack/react-router'
import { useServerFn } from '@tanstack/react-start'
import { motion } from 'motion/react'
import { toast } from 'sonner'
import { appConfig } from '#/app.config'
import { EmptyState } from '#/components/dashboard/EmptyState'
import { PollCard } from '#/components/dashboard/PollCard'
import { m } from '#/lib/i18n'
import { staggerContainer, staggerItem } from '#/lib/motion'
import { deletePoll, duplicatePoll, listMyPolls } from '#/server/polls/polls.functions'

export const Route = createFileRoute('/dashboard')({
  beforeLoad: ({ context }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: '/dashboard' } })
    }
  },
  loader: () => listMyPolls(),
  head: () => ({
    meta: [{ title: `${m.dashboard_title()} — ${appConfig.name}` }],
  }),
  component: DashboardPage,
})

function DashboardPage() {
  const polls = Route.useLoaderData()
  const router = useRouter()
  const navigate = Route.useNavigate()
  const duplicateFn = useServerFn(duplicatePoll)
  const deleteFn = useServerFn(deletePoll)

  async function handleDuplicate(pollId: string) {
    try {
      const { id } = await duplicateFn({ data: { pollId } })
      toast.success(m.poll_duplicated())
      await navigate({ to: '/p/$id', params: { id } })
    } catch {
      toast.error(m.poll_error_generic())
    }
  }

  async function handleDelete(pollId: string) {
    try {
      await deleteFn({ data: { pollId } })
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
          {polls.map((poll) => (
            <motion.li key={poll.id} variants={staggerItem}>
              <PollCard
                poll={poll}
                onDuplicate={() => handleDuplicate(poll.id)}
                onDelete={() => handleDelete(poll.id)}
              />
            </motion.li>
          ))}
        </motion.ul>
      )}
    </div>
  )
}
