import { createFileRoute, redirect, useRouter } from '@tanstack/react-router'
import { HandleField } from '#/components/booking/HandleField'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'
import { Separator } from '#/components/ui/separator'
import { m } from '#/lib/i18n'
import { setHandle } from '#/api/bookings'

export const Route = createFileRoute('/settings')({
  beforeLoad: ({ context }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: '/settings' } })
    }
  },
  component: SettingsPage,
})

function SettingsPage() {
  const { session } = Route.useRouteContext()
  if (!session) return null

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-8 px-5 py-12 sm:py-16">
      <div>
        <h1 className="display text-3xl">{m.settings_title()}</h1>
      </div>

      <ProfileSection name={session.user.name} email={session.user.email} />

      <Separator />

      {session.org && (
        <>
          <section>
            <HandleSection handle={session.org.slug} appUrl={window.location.origin} />
          </section>

          <Separator />
        </>
      )}

      <section className="flex flex-col gap-3">
        <div>
          <h2 className="text-sm font-semibold">{m.settings_language_title()}</h2>
          <p className="text-sm text-muted-foreground">{m.settings_language_subtitle()}</p>
        </div>
        <LocaleSwitcher />
      </section>
    </div>
  )
}

/**
 * Read-only for now: there is no profile-update route in `internal/auth/routes.txt` at all (Limen's
 * credential-password plugin only ever reads `email`/`password` off a signup/signin body — see
 * `#/api/auth.ts`'s own doc comment), so a name typed here has nowhere to save to. Kept as its own
 * section (rather than deleted outright, the way billing/passkeys were) since email display is
 * still useful, and a future task adding a profile-update endpoint only needs to add the form back.
 */
function ProfileSection({ name, email }: { name: string; email: string }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-sm font-semibold">{m.settings_profile_title()}</h2>
      <p className="text-sm font-medium">{name}</p>
      <p className="text-sm text-muted-foreground">{email}</p>
    </section>
  )
}

/** Wires `HandleField` (kept server-free so it can be unit-tested) to the Go `setHandle` API call,
 * refreshing the session afterwards so every booking link picks the new handle up. */
function HandleSection({ handle, appUrl }: { handle: string | null; appUrl: string }) {
  const router = useRouter()

  return (
    <HandleField
      currentHandle={handle}
      appUrl={appUrl}
      onSave={async (next) => {
        await setHandle(next)
        await router.invalidate()
      }}
    />
  )
}
