import { createFileRoute, Link } from '@tanstack/react-router'
import { fetchAdminStats } from '#/api/admin'
import { m } from '#/lib/i18n'

export const Route = createFileRoute('/admin/')({
  loader: () => fetchAdminStats(),
  component: AdminDashboard,
})

function StatCard({ label, value, recent }: { label: string; value: number; recent?: number }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border p-4">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="text-2xl tabular-nums">{value.toLocaleString()}</span>
      {recent !== undefined && (
        <span className="text-xs text-muted-foreground">
          {m.admin_stat_recent({ count: recent })}
        </span>
      )}
    </div>
  )
}

/**
 * `internal/admin/stats.go`'s `DashboardStats` is flat (no `growth`/`revenue` nesting) and has no
 * `revenue` block at all — billing is gone from this rewrite, so subscriptions/MRR/premium-org
 * counts have no source of truth left to read from. It adds `mailQueueDepth`/`failedJobs`
 * (scheduled_jobs-backed, no TS equivalent existed).
 */
function AdminDashboard() {
  const stats = Route.useLoaderData()

  return (
    <div className="flex flex-col gap-8">
      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label={m.admin_stat_users()}
          value={stats.users.total}
          recent={stats.users.last7}
        />
        <StatCard label={m.admin_stat_orgs()} value={stats.orgs.total} recent={stats.orgs.last7} />
        <StatCard
          label={m.admin_stat_polls()}
          value={stats.polls.total}
          recent={stats.polls.last7}
        />
        <StatCard label={m.admin_stat_polls_finalized()} value={stats.pollsFinalized} />
        <StatCard
          label={m.admin_stat_signups()}
          value={stats.signupSheets.total}
          recent={stats.signupSheets.last7}
        />
        <StatCard
          label={m.admin_stat_booking_pages()}
          value={stats.bookingPages.total}
          recent={stats.bookingPages.last7}
        />
        <StatCard
          label={m.admin_stat_bookings()}
          value={stats.bookings.total}
          recent={stats.bookings.last7}
        />
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-semibold">{m.admin_mail_title()}</h2>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard label={m.admin_mail_queue_depth()} value={stats.mailQueueDepth} />
          <StatCard label={m.admin_mail_failed_jobs()} value={stats.failedJobs} />
        </div>
        {/* The count alone is a dead end — the whole point of surfacing failed jobs (spec §5) is
            that an operator can see WHAT failed and resend it. */}
        <Link to="/admin/jobs" className="text-sm underline underline-offset-2">
          {m.admin_mail_view_failed()}
        </Link>
      </section>
    </div>
  )
}
