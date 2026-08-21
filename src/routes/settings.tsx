import { useEffect, useState, type FormEvent } from 'react'
import { createFileRoute, redirect, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import { PasskeyManager } from '#/components/auth/PasskeyManager'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '#/components/ui/dialog'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Separator } from '#/components/ui/separator'
import { m } from '#/lib/i18n'
import { authClient } from '#/server/auth/client'

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

      <section className="flex flex-col gap-3">
        <div>
          <h2 className="text-sm font-semibold">{m.settings_language_title()}</h2>
          <p className="text-sm text-muted-foreground">{m.settings_language_subtitle()}</p>
        </div>
        <LocaleSwitcher />
      </section>

      <Separator />

      <section>
        <PasskeyManager />
      </section>

      <Separator />

      <DangerZone />
    </div>
  )
}

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
      const { error } = await authClient.updateUser({ name: trimmed })
      if (error) {
        toast.error(m.auth_error_generic())
        return
      }
      toast.success(m.settings_name_saved())
      await router.invalidate()
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <h2 className="text-sm font-semibold">{m.settings_profile_title()}</h2>
      <form
        onSubmit={(e) => void handleSubmit(e)}
        className="flex flex-col gap-3 sm:flex-row sm:items-end"
      >
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="settings-name">{m.settings_name_label()}</Label>
          <Input
            id="settings-name"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            maxLength={80}
          />
        </div>
        <Button type="submit" disabled={saving || !value.trim() || value.trim() === name}>
          {m.settings_name_save()}
        </Button>
      </form>
      <p className="text-sm text-muted-foreground">{email}</p>
    </section>
  )
}

function DangerZone() {
  const router = useRouter()
  const navigate = useNavigate()

  const [hasPassword, setHasPassword] = useState<boolean | null>(null)
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const [deleting, setDeleting] = useState(false)

  useEffect(() => {
    void authClient
      .listAccounts()
      .then(({ data }) => setHasPassword((data ?? []).some((a) => a.providerId === 'credential')))
  }, [])

  async function handleDelete(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setDeleting(true)
    try {
      const { data, error } = await authClient.deleteUser(
        hasPassword ? { password } : { callbackURL: '/' },
      )
      if (error) {
        toast.error(m.settings_delete_error())
        return
      }
      // Better-Auth deletes immediately when `sendDeleteAccountVerification` isn't configured
      // (the current setup); it only replies "Verification email sent" once that's wired up, so
      // this branch keeps the UI correct either way instead of assuming which path ran.
      if ((data as { message?: string } | null)?.message === 'Verification email sent') {
        toast.success(m.settings_delete_verify_sent())
        setOpen(false)
        return
      }
      toast.success(m.settings_delete_success())
      await router.invalidate()
      await navigate({ to: '/' })
    } finally {
      setDeleting(false)
    }
  }

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-destructive/30 bg-destructive/5 p-4">
      <div>
        <h2 className="text-sm font-semibold text-destructive">{m.settings_danger_title()}</h2>
        <p className="text-sm text-muted-foreground">{m.settings_danger_subtitle()}</p>
      </div>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next)
          if (!next) setPassword('')
        }}
      >
        <DialogTrigger asChild>
          <Button type="button" variant="destructive" className="w-fit">
            {m.settings_delete_account()}
          </Button>
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{m.settings_delete_dialog_title()}</DialogTitle>
            <DialogDescription>
              {hasPassword
                ? m.settings_delete_dialog_body_password()
                : m.settings_delete_dialog_body_oauth()}
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={(e) => void handleDelete(e)} className="flex flex-col gap-4">
            {hasPassword && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="delete-password">{m.settings_delete_password_label()}</Label>
                <Input
                  id="delete-password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </div>
            )}
            <DialogFooter>
              <Button
                type="submit"
                variant="destructive"
                disabled={deleting || hasPassword === null || (hasPassword && !password)}
              >
                {deleting ? m.settings_delete_deleting() : m.settings_delete_confirm()}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </section>
  )
}
