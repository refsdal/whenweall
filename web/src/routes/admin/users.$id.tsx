import { createFileRoute, Link } from '@tanstack/react-router'
import { fetchAdminUserDetail } from '#/server/admin/admin.functions'
import { Badge } from '#/components/ui/badge'
import { m } from '#/lib/i18n'

export const Route = createFileRoute('/admin/users/$id')({
  loader: ({ params }) => fetchAdminUserDetail({ data: { userId: params.id } }),
  component: AdminUserDetailPage,
})

function Field({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-sm">{value}</span>
    </div>
  )
}

function AdminUserDetailPage() {
  const detail = Route.useLoaderData()

  if (!detail) {
    return <p className="text-sm text-muted-foreground">{m.admin_user_not_found()}</p>
  }

  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-4 rounded-lg border p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg">{detail.name}</h2>
          {detail.role === 'staff' && <Badge>{m.admin_badge_staff()}</Badge>}
          {detail.banned && <Badge variant="destructive">{m.admin_badge_banned()}</Badge>}
          {!detail.emailVerified && <Badge variant="outline">{m.admin_badge_unverified()}</Badge>}
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={m.admin_col_email()} value={detail.email} />
          <Field
            label={m.admin_col_created()}
            value={new Date(detail.createdAt).toLocaleDateString()}
          />
          {detail.banReason && <Field label={m.admin_badge_banned()} value={detail.banReason} />}
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_user_orgs()}</h3>
        {detail.orgs.length === 0 ? (
          <p className="text-sm text-muted-foreground">—</p>
        ) : (
          <ul className="flex flex-col gap-1 text-sm">
            {detail.orgs.map((org) => (
              <li key={org.id} className="flex items-center gap-2">
                <span>{org.name}</span>
                <Badge variant="outline">{org.role}</Badge>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_user_content()}</h3>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={m.admin_stat_polls()} value={detail.counts.polls} />
          <Field label={m.admin_stat_booking_pages()} value={detail.counts.bookingPages} />
          <Field label={m.admin_stat_bookings()} value={detail.counts.bookings} />
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_user_history()}</h3>
        {detail.recentActions.length === 0 ? (
          <p className="text-sm text-muted-foreground">{m.admin_audit_empty()}</p>
        ) : (
          <ul className="flex flex-col gap-1 text-sm">
            {detail.recentActions.map((entry) => (
              <li key={entry.id} className="flex flex-wrap items-center gap-2">
                <span className="text-muted-foreground">
                  {new Date(entry.createdAt).toLocaleString()}
                </span>
                <span className="font-mono text-xs">{entry.action}</span>
                <span className="text-muted-foreground">{entry.actorEmail}</span>
                {entry.reason && <span>“{entry.reason}”</span>}
              </li>
            ))}
          </ul>
        )}
      </section>

      <Link to="/admin/users" className="text-sm underline underline-offset-2">
        ← {m.admin_nav_users()}
      </Link>
    </div>
  )
}
