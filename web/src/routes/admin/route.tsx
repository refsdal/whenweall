import { createFileRoute, Link, notFound, Outlet } from '@tanstack/react-router'
import { appConfig } from '#/app.config'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

/**
 * Chrome and navigation guard for the whole console.
 *
 * `notFound()` rather than a 403: there is no reason to confirm to a stranger that an admin area
 * exists here. This is navigation only — every admin server function re-checks with
 * `requireStaffMiddleware`, and that is the gate that actually matters.
 */
export const Route = createFileRoute('/admin')({
  beforeLoad: ({ context }) => {
    if (!context.session?.isStaff) throw notFound()
  },
  head: () => ({
    meta: [
      { title: `${m.admin_title()} — ${appConfig.name}` },
      { name: 'robots', content: 'noindex' },
    ],
  }),
  component: AdminLayout,
})

const TABS = [
  { to: '/admin', label: () => m.admin_nav_dashboard(), exact: true },
  { to: '/admin/users', label: () => m.admin_nav_users(), exact: false },
  { to: '/admin/audit', label: () => m.admin_nav_audit(), exact: false },
] as const

function AdminLayout() {
  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-5 py-10">
      <header className="flex flex-col gap-4">
        <h1 className="display text-2xl">{m.admin_title()}</h1>
        <nav className="flex gap-1 border-b">
          {TABS.map((tab) => (
            <Link
              key={tab.to}
              to={tab.to}
              activeOptions={{ exact: tab.exact }}
              className={cn(
                'rounded-t-md px-3 py-2 text-sm text-muted-foreground hover:text-foreground',
                'data-[status=active]:border-b-2 data-[status=active]:border-primary data-[status=active]:text-foreground',
              )}
            >
              {tab.label()}
            </Link>
          ))}
        </nav>
      </header>
      <Outlet />
    </div>
  )
}
