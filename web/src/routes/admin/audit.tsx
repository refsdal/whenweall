import { createFileRoute } from '@tanstack/react-router'
import { fetchAuditLog } from '#/api/admin'
import { m } from '#/lib/i18n'

export const Route = createFileRoute('/admin/audit')({
  loader: () => fetchAuditLog({ limit: 100 }),
  component: AdminAudit,
})

function AdminAudit() {
  const { entries } = Route.useLoaderData()

  if (entries.length === 0) {
    return <p className="text-sm text-muted-foreground">{m.admin_audit_empty()}</p>
  }

  return (
    <div className="overflow-x-auto rounded-lg border">
      <table className="w-full text-sm">
        <thead className="border-b text-left text-muted-foreground">
          <tr>
            <th className="p-3 font-medium">{m.admin_audit_col_when()}</th>
            <th className="p-3 font-medium">{m.admin_audit_col_actor()}</th>
            <th className="p-3 font-medium">{m.admin_audit_col_action()}</th>
            <th className="p-3 font-medium">{m.admin_audit_col_target()}</th>
            <th className="p-3 font-medium">{m.admin_audit_col_reason()}</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry) => (
            <tr key={entry.id} className="border-b last:border-0">
              <td className="p-3 whitespace-nowrap text-muted-foreground">
                {new Date(entry.createdAt).toLocaleString()}
              </td>
              <td className="p-3">{entry.actorEmail}</td>
              <td className="p-3 font-mono text-xs" data-action={entry.action}>
                {entry.action}
              </td>
              <td
                className="p-3 font-mono text-xs text-muted-foreground"
                data-target={entry.targetId ?? undefined}
              >
                {entry.targetId ?? '—'}
              </td>
              <td className="p-3">
                {entry.reason ?? (
                  // Anything not done through the console arrives without one. Rendered
                  // distinctly rather than blank, because a cluster of these is worth noticing.
                  <span className="text-muted-foreground italic">{m.admin_audit_no_reason()}</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
