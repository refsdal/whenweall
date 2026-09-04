import { createFileRoute, useRouter } from '@tanstack/react-router'
import { fetchFailedJobs } from '#/api/admin'
import { FailedJobsTable } from '#/components/admin/FailedJobsTable'
import { m } from '#/lib/i18n'

/**
 * The dead-letter screen (spec §5). Staff-gated by the parent `/admin` layout's `beforeLoad`
 * (route.tsx) for navigation, and by `RequireStaff` on every request underneath — the gate that
 * actually matters. The loader re-runs on `router.invalidate()`, which is how a successful Retry
 * makes the row disappear (the worker claims it within its poll interval; until then it is
 * simply no longer dead-lettered and drops out of GET jobs/failed).
 */
export const Route = createFileRoute('/admin/jobs')({
  loader: () => fetchFailedJobs(),
  component: AdminJobs,
})

function AdminJobs() {
  const jobs = Route.useLoaderData()
  const router = useRouter()

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">{m.admin_jobs_intro()}</p>
      <FailedJobsTable jobs={jobs} onRetried={() => router.invalidate()} />
    </div>
  )
}
