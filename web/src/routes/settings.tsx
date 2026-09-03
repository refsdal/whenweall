import { useState, type FormEvent } from 'react'
import { createFileRoute, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import { DeleteAccountDialog } from '#/components/auth/DeleteAccountDialog'
import { HandleField } from '#/components/booking/HandleField'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Separator } from '#/components/ui/separator'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { requireVerifiedSession } from '#/lib/session-guard'
import { setHandle } from '#/api/bookings'
import { deleteOwnAccount, myOrgRoles, updateProfile } from '#/api/auth'

export const Route = createFileRoute('/settings')({
  beforeLoad: ({ context }) => requireVerifiedSession(context, '/settings'),
  // The caller's own membership roles in the active org — see HandleSection's doc comment for
  // why this gates the org-handle editor's visibility rather than just its submit handler.
  loader: () => myOrgRoles(),
  component: SettingsPage,
})

function SettingsPage() {
  const { session } = Route.useRouteContext()
  const orgRoles = Route.useLoaderData()
  const router = useRouter()
  const navigate = useNavigate()
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

      <Separator />

      <DeleteAccountDialog
        hasPassword={session.user.hasPassword}
        onDelete={async (password) => {
          try {
            await deleteOwnAccount(password)
          } catch (error) {
            // invalid_password / password_required: the dialog stays open for another try.
            toast.error(
              errorCode(error) === 'invalid_password' || errorCode(error) === 'password_required'
                ? m.settings_delete_error()
                : m.auth_error_generic(),
            )
            return
          }
          toast.success(m.settings_delete_success())
          await router.invalidate()
          await navigate({ to: '/' })
        }}
      />
    </div>
  )
}

/** Editable display name (PATCH /api/v1/me); the email is read-only — it is the account's identity. */
function ProfileSection({ name, email }: { name: string; email: string }) {
  const router = useRouter()
  const [value, setValue] = useState(name)
  const [saving, setSaving] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = value.trim()
    if (!trimmed || trimmed.length > 80) return

    setSaving(true)
    try {
      await updateProfile({ name: trimmed })
      toast.success(m.settings_name_saved())
      await router.invalidate()
    } catch {
      toast.error(m.auth_error_generic())
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-sm font-semibold">{m.settings_profile_title()}</h2>
      <form onSubmit={(e) => void handleSubmit(e)} className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="settings-name">{m.settings_name_label()}</Label>
          <Input id="settings-name" value={value} onChange={(e) => setValue(e.target.value)} maxLength={80} />
        </div>
        <Button type="submit" disabled={saving || !value.trim() || value.trim() === name}>
          {m.settings_name_save()}
        </Button>
      </form>
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
