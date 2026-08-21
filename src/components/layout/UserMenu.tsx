import { Link, useNavigate, useRouter } from '@tanstack/react-router'
import { LayoutDashboard, LogOut, Settings } from 'lucide-react'
import { m } from '#/lib/i18n'
import { authClient } from '#/server/auth/client'
import type { ClientSession } from '#/server/auth/session.functions'
import { buttonVariants } from '#/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '#/components/ui/dropdown-menu'
import { cn } from '#/lib/utils'

function initial(name: string | null, email: string): string {
  const source = name?.trim() || email
  return source.slice(0, 1).toUpperCase()
}

export function UserMenu({ session }: { session: ClientSession }) {
  const router = useRouter()
  const navigate = useNavigate()

  if (!session) {
    return (
      <Link to="/login" className={cn(buttonVariants({ variant: 'ghost', size: 'sm' }))}>
        {m.nav_sign_in()}
      </Link>
    )
  }

  const { user } = session

  async function handleSignOut() {
    await authClient.signOut()
    await router.invalidate()
    await navigate({ to: '/' })
  }

  return (
    <DropdownMenu>
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
        {/* `/dashboard` arrives with task 18-19; plain anchor until then. */}
        <DropdownMenuItem asChild>
          <a href="/dashboard">
            <LayoutDashboard aria-hidden="true" />
            {m.nav_dashboard()}
          </a>
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to="/settings">
            <Settings aria-hidden="true" />
            {m.nav_settings()}
          </Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void handleSignOut()}>
          <LogOut aria-hidden="true" />
          {m.nav_sign_out()}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
