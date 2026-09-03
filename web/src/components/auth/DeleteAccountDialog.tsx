import { useState, type FormEvent } from 'react'
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
import { m } from '#/lib/i18n'

/**
 * The settings page's danger zone (port of main:src/routes/settings.tsx's DangerZone). A
 * credential account confirms with its current password — DELETE /api/v1/me re-checks it
 * server-side (400 password_required / 403 invalid_password); an OAuth-only account has nothing
 * to re-enter and just confirms. Router-free: the route wires `onDelete` to the API call and the
 * post-deletion navigation.
 */
export function DeleteAccountDialog({
  hasPassword,
  onDelete,
}: {
  hasPassword: boolean
  onDelete: (password: string | undefined) => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [password, setPassword] = useState('')
  const [deleting, setDeleting] = useState(false)

  async function handleDelete(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setDeleting(true)
    try {
      await onDelete(hasPassword ? password : undefined)
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
              {hasPassword ? m.settings_delete_dialog_body_password() : m.settings_delete_dialog_body_oauth()}
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
              <Button type="submit" variant="destructive" disabled={deleting || (hasPassword && !password)}>
                {deleting ? m.settings_delete_deleting() : m.settings_delete_confirm()}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </section>
  )
}
