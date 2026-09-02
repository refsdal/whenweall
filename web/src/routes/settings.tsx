import { createFileRoute, redirect, useRouter } from '@tanstack/react-router'
import { HandleField } from '#/components/booking/HandleField'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'
import { Separator } from '#/components/ui/separator'
import { m } from '#/lib/i18n'
import { setHandle } from '#/api/bookings'
import { myOrgRoles } from '#/api/auth'

export const Route = createFileRoute('/settings')({
  beforeLoad: ({ context }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: '/settings' } })
    }
  },
  // The caller's own membership roles in the active org — see HandleSection's doc comment for
  // why this gates the org-handle editor's visibility rather than just its submit handler.
  loader: () => myOrgRoles(),
  component: SettingsPage,
})

function SettingsPage() {
  const { session } = Route.useRouteContext()
  const orgRoles = Route.useLoaderData()
  if (!session) return null

  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col gap-8 px-5 py-12 sm:py-16">
      <div>
        <h1 className="display text-3xl">{m.settings_title()}</h1>
      </div>

      <ProfileSection name={session.user.name} email={session.user.email} />

      <Separator />

      {session.org && orgRoles.includes('owner') && (
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

/**
 * Wires `HandleField` (kept server-free so it can be unit-tested) to the Go `setHandle` API call,
 * refreshing the session afterwards so every booking link picks the new handle up.
 *
 * Only ever rendered for an org owner — `SettingsPage` gates it on `myOrgRoles()` including
 * `"owner"` — since `POST /api/v1/org/handle` is itself gated server-side by `RequireOwnerRole`
 * (internal/bookings/authz.go): a non-owner member used to see this same editable field and only
 * find out it wasn't theirs to change from a 403 toast after submitting.
 */
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
