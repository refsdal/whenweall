import { useRouteContext } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { buttonVariants } from '#/components/ui/button'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'
import { Logo } from '#/components/layout/Logo'
import { ThemeToggle } from '#/components/layout/ThemeToggle'
import { UserMenu } from '#/components/layout/UserMenu'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'

export function Header() {
  const { session } = useRouteContext({ from: '__root__' })

  return (
    <header className="sticky top-0 z-40 border-b border-border/60 bg-background/75 backdrop-blur-md">
      <div className="mx-auto flex h-16 w-full max-w-6xl items-center justify-between gap-3 px-5 sm:px-8">
        <Logo />

        <nav className="flex items-center gap-1.5 sm:gap-2.5">
          <LocaleSwitcher className="hidden sm:inline-flex" />
          <ThemeToggle />
          {/* `/new` arrives with task 17; plain anchor until the route exists. */}
          <a
            href="/new"
            className={cn(buttonVariants({ size: 'sm' }), 'gap-1.5 pl-3.5 max-sm:h-9 max-sm:px-3')}
          >
            <Plus aria-hidden="true" />
            <span className="max-sm:sr-only">{m.nav_new_poll()}</span>
          </a>
          <UserMenu session={session} />
        </nav>
      </div>
    </header>
  )
}
