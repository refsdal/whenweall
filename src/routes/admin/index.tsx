import { createFileRoute } from '@tanstack/react-router'
import { fetchAdminStats } from '#/server/admin/admin.functions'
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

/** Amounts are stored in minor units, as Stripe reports them. */
function formatMinor(minor: number): string {
  return `${(minor / 100).toLocaleString()} NOK`
}

function AdminDashboard() {
  const { growth, revenue } = Route.useLoaderData()

  return (
    <div className="flex flex-col gap-8">
      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          label={m.admin_stat_users()}
          value={growth.users.total}
          recent={growth.users.last7}
        />
        <StatCard
          label={m.admin_stat_orgs()}
          value={growth.orgs.total}
          recent={growth.orgs.last7}
        />
        <StatCard
          label={m.admin_stat_polls()}
          value={growth.polls.total}
          recent={growth.polls.last7}
        />
        <StatCard label={m.admin_stat_polls_finalized()} value={growth.pollsFinalized} />
        <StatCard
          label={m.admin_stat_signups()}
          value={growth.signupSheets.total}
          recent={growth.signupSheets.last7}
        />
        <StatCard
          label={m.admin_stat_booking_pages()}
          value={growth.bookingPages.total}
          recent={growth.bookingPages.last7}
        />
        <StatCard
          label={m.admin_stat_bookings()}
          value={growth.bookings.total}
          recent={growth.bookings.last7}
        />
      </section>

      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-semibold">{m.admin_revenue_title()}</h2>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard label={m.admin_revenue_premium_orgs()} value={revenue.premiumOrgs} />
          <StatCard label={m.admin_revenue_active_subs()} value={revenue.activeSubscriptions} />
          <div className="flex flex-col gap-1 rounded-lg border p-4">
            <span className="text-sm text-muted-foreground">{m.admin_revenue_mrr()}</span>
            <span className="text-2xl tabular-nums">{formatMinor(revenue.mrrMinor)}</span>
          </div>
          <StatCard label={m.admin_revenue_cancelling()} value={revenue.cancellingAtPeriodEnd} />
        </div>
      </section>
    </div>
  )
}
