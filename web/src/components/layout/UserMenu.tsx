import { useState } from 'react'
import { Link, useNavigate, useRouter } from '@tanstack/react-router'
import { Building2, CalendarClock, LayoutDashboard, LogOut, Settings } from 'lucide-react'
import { toast } from 'sonner'
import { m } from '#/lib/i18n'
import { listOrganizations, signOut, switchOrganization, type OrgSummary } from '#/api/auth'
import type { Session } from '#/lib/use-session'
import { buttonVariants } from '#/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from '#/components/ui/dropdown-menu'
import { cn } from '#/lib/utils'

function initial(name: string | null, email: string): string {
  const source = name?.trim() || email
  return source.slice(0, 1).toUpperCase()
}

export function UserMenu({ session }: { session: Session }) {
  const router = useRouter()
  const navigate = useNavigate()
  // Loaded when the menu opens, not on every page render: the header is on every page and most
  // users have exactly one organization (their personal one), for which no switcher is shown.
  const [orgs, setOrgs] = useState<OrgSummary[] | null>(null)

  if (!session) {
    return (
      <Link to="/login" className={cn(buttonVariants({ variant: 'ghost', size: 'sm' }))}>
        {m.nav_sign_in()}
      </Link>
    )
  }

  const { user } = session

  async function handleSignOut() {
    await signOut()
    await router.invalidate()
    await navigate({ to: '/' })
  }

  async function loadOrgs() {
    if (!user.emailVerified) return // GET /api/v1/me/organizations is verified-only
    try {
      setOrgs(await listOrganizations())
    } catch {
      setOrgs([])
    }
  }

  async function handleSwitch(orgId: string) {
    const target = orgs?.find((org) => org.id === orgId)
    if (!target || target.active) return
    try {
      await switchOrganization(orgId)
      await router.invalidate()
      toast.success(m.nav_org_switched({ name: target.name }))
    } catch {
      toast.error(m.nav_org_switch_failed())
    }
  }

  const activeOrg = orgs?.find((org) => org.active)

  return (
    <DropdownMenu onOpenChange={(open) => open && void loadOrgs()}>
      <DropdownMenuTrigger
        aria-label={m.nav_account_menu()}
        className={cn(
          'focus-ring inline-flex size-9 items-center justify-center rounded-full bg-accent-soft text-sm font-semibold text-accent-foreground',
          'transition-transform duration-200 hover:scale-105 data-[state=open]:scale-105',
        )}
      >
        {initial(user.name, user.email)}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="font-normal">
          <span className="block truncate text-sm font-medium">{user.name || user.email}</span>
          <span className="block truncate text-xs text-muted-foreground">{user.email}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/dashboard">
            <LayoutDashboard aria-hidden="true" />
            {m.nav_dashboard()}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to="/bookings">
            <CalendarClock aria-hidden="true" />
            {m.nav_booking_pages()}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to="/settings">
            <Settings aria-hidden="true" />
            {m.nav_settings()}
          </Link>
        </DropdownMenuItem>
        {orgs && orgs.length > 1 && (
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <Building2 aria-hidden="true" />
              <span className="truncate">{activeOrg?.name ?? m.nav_organizations()}</span>
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              <DropdownMenuLabel>{m.nav_organizations()}</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={activeOrg?.id} onValueChange={(id) => void handleSwitch(id)}>
                {orgs.map((org) => (
                  <DropdownMenuRadioItem key={org.id} value={org.id}>
                    <span className="truncate">{org.name}</span>
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void handleSignOut()}>
          <LogOut aria-hidden="true" />
          {m.nav_sign_out()}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
