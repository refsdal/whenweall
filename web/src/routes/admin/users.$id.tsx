import { createFileRoute, Link, useRouter } from '@tanstack/react-router'
import { fetchAdminUserDetail, fetchAuditLog } from '#/api/admin'
import { UserActions } from '#/components/admin/UserActions'
import { Badge } from '#/components/ui/badge'
import { m } from '#/lib/i18n'
import { useSession } from '#/lib/use-session'

export const Route = createFileRoute('/admin/users/$id')({
  loader: async ({ params }) => {
    const detail = await fetchAdminUserDetail(params.id)
    // `AdminUserDetail` (internal/admin/users.go) no longer carries `recentActions` — the console
    // reads that off the shared audit endpoint instead, filtered to this user (see
    // `internal/admin/handlers.go`'s own package doc comment).
    const audit = detail
      ? await fetchAuditLog({ targetType: 'user', targetId: params.id, limit: 20 })
      : null
    return { detail, recentActions: audit?.entries ?? [] }
  },
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
  const { detail, recentActions } = Route.useLoaderData()
  const router = useRouter()
  const navigate = Route.useNavigate()
  const session = useSession()

  if (!detail) {
    return <p className="text-sm text-muted-foreground">{m.admin_user_not_found()}</p>
  }

  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-4 rounded-lg border p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg">{detail.name}</h2>
          {detail.staff && <Badge>{m.admin_badge_staff()}</Badge>}
          {detail.locked && <Badge variant="destructive">{m.admin_badge_locked()}</Badge>}
          {!detail.emailVerified && <Badge variant="outline">{m.admin_badge_unverified()}</Badge>}
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={m.admin_col_email()} value={detail.email} />
          <Field
            label={m.admin_col_created()}
            value={new Date(detail.createdAt).toLocaleDateString()}
          />
          {detail.lockReason && <Field label={m.admin_badge_locked()} value={detail.lockReason} />}
        </div>
      </section>

      {/* Lock/unlock refetch this page's loader (the row changes); delete leaves for the list,
          since there is no row left to reload — see UserActions' own doc comment. */}
      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_actions_title()}</h3>
        <UserActions
          user={detail}
          isSelf={session?.user.id === detail.id}
          onChanged={() => router.invalidate()}
          onDeleted={() => navigate({ to: '/admin/users' })}
        />
      </section>

      <section className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{m.admin_user_orgs()}</h3>
        {detail.orgs.length === 0 ? (
          <p className="text-sm text-muted-foreground">—</p>
        ) : (
          <ul className="flex flex-col gap-1 text-sm">
            {detail.orgs.map((org) => (
              <li key={org.id} className="flex flex-wrap items-center gap-2">
                <span>{org.name}</span>
                {org.roles.map((role) => (
                  <Badge key={role} variant="outline">
                    {role}
                  </Badge>
                ))}
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
        {recentActions.length === 0 ? (
          <p className="text-sm text-muted-foreground">{m.admin_audit_empty()}</p>
        ) : (
          <ul className="flex flex-col gap-1 text-sm">
            {recentActions.map((entry) => (
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
