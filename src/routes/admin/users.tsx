import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import * as z from 'zod'
import { fetchAdminUsers } from '#/server/admin/admin.functions'
import { Badge } from '#/components/ui/badge'
import { Input } from '#/components/ui/input'
import { m } from '#/lib/i18n'

const PAGE_SIZE = 50

export const Route = createFileRoute('/admin/users')({
  validateSearch: z.object({ q: z.string().optional(), page: z.number().int().min(0).optional() }),
  loaderDeps: ({ search }) => ({ q: search.q, page: search.page ?? 0 }),
  loader: ({ deps }) =>
    fetchAdminUsers({
      data: { search: deps.q, limit: PAGE_SIZE, offset: deps.page * PAGE_SIZE },
    }),
  component: AdminUsers,
})

function AdminUsers() {
  const { users, total } = Route.useLoaderData()
  const { q } = Route.useSearch()
  const navigate = Route.useNavigate()
  const [draft, setDraft] = useState(q ?? '')

  return (
    <div className="flex flex-col gap-4">
      <form
        className="flex items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault()
          void navigate({ search: { q: draft || undefined, page: 0 } })
        }}
      >
        <Input
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          placeholder={m.admin_users_search()}
          aria-label={m.admin_users_search()}
        />
      </form>

      <p className="text-sm text-muted-foreground">{m.admin_users_count({ total })}</p>

      {users.length === 0 ? (
        <p className="text-sm text-muted-foreground">{m.admin_users_empty()}</p>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="border-b text-left text-muted-foreground">
              <tr>
                <th className="p-3 font-medium">{m.admin_col_name()}</th>
                <th className="p-3 font-medium">{m.admin_col_email()}</th>
                <th className="p-3 font-medium">{m.admin_col_role()}</th>
                <th className="p-3 font-medium">{m.admin_col_created()}</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id} className="border-b last:border-0">
                  <td className="p-3">
                    <Link
                      to="/admin/users/$id"
                      params={{ id: u.id }}
                      className="underline underline-offset-2"
                    >
                      {u.name}
                    </Link>
                  </td>
                  <td className="p-3 text-muted-foreground">{u.email}</td>
                  <td className="flex flex-wrap gap-1 p-3">
                    {u.role === 'staff' && <Badge>{m.admin_badge_staff()}</Badge>}
                    {u.banned && <Badge variant="destructive">{m.admin_badge_banned()}</Badge>}
                    {!u.emailVerified && (
                      <Badge variant="outline">{m.admin_badge_unverified()}</Badge>
                    )}
                  </td>
                  <td className="p-3 text-muted-foreground">
                    {new Date(u.createdAt).toLocaleDateString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
