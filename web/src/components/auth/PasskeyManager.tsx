import { useEffect, useState, type FormEvent } from 'react'
import { Fingerprint, KeyRound, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
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
import { authClient } from '#/server/auth/client'

type PasskeyRow = { id: string; name?: string | null }

/**
 * Lists the signed-in user's passkeys and lets them add or remove one. Deleting asks for an
 * inline confirm (click "Delete" reveals "Confirm"/"Cancel") rather than a native `confirm()` so
 * it stays keyboard- and screen-reader-friendly; adding opens a small dialog for an optional
 * device name.
 */
export function PasskeyManager() {
  const [passkeys, setPasskeys] = useState<PasskeyRow[] | null>(null)
  const [confirmingId, setConfirmingId] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [adding, setAdding] = useState(false)

  async function refresh() {
    const { data } = await authClient.passkey.listUserPasskeys()
    setPasskeys(data ?? [])
  }

  useEffect(() => {
    void authClient.passkey.listUserPasskeys().then(({ data }) => setPasskeys(data ?? []))
  }, [])

  async function handleAdd(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setAdding(true)
    try {
      const { error } = await authClient.passkey.addPasskey({ name: newName || undefined })
      if (error) {
        toast.error(m.auth_passkey_add_error())
        return
      }
      toast.success(m.auth_passkey_add_success())
      setAddOpen(false)
      setNewName('')
      await refresh()
    } finally {
      setAdding(false)
    }
  }

  async function handleDelete(id: string) {
    setDeletingId(id)
    try {
      const { error } = await authClient.passkey.deletePasskey({ id })
      if (error) {
        toast.error(m.auth_passkey_delete_error())
        return
      }
      setConfirmingId(null)
      await refresh()
    } finally {
      setDeletingId(null)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold">{m.auth_passkeys_title()}</h3>
          <p className="text-sm text-muted-foreground">{m.auth_passkeys_subtitle()}</p>
        </div>
        <Dialog open={addOpen} onOpenChange={setAddOpen}>
          <DialogTrigger asChild>
            <Button type="button" variant="outline" size="sm">
              <Fingerprint aria-hidden="true" />
              {m.auth_passkey_add()}
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{m.auth_passkey_add_title()}</DialogTitle>
              <DialogDescription>{m.auth_passkey_add_description()}</DialogDescription>
            </DialogHeader>
            <form onSubmit={(e) => void handleAdd(e)} className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="passkey-name">{m.auth_passkey_name_label()}</Label>
                <Input
                  id="passkey-name"
                  value={newName}
                  onChange={(event) => setNewName(event.target.value)}
                  placeholder={m.auth_passkey_name_placeholder()}
                  autoFocus
                />
              </div>
              <DialogFooter>
                <Button type="submit" disabled={adding}>
                  {m.auth_passkey_add_confirm()}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>

      {passkeys === null ? (
        <p className="text-sm text-muted-foreground">{m.auth_passkeys_loading()}</p>
      ) : passkeys.length === 0 ? (
        <p className="text-sm text-muted-foreground">{m.auth_passkeys_empty()}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {passkeys.map((passkey) => {
            const label = passkey.name || m.auth_passkey_unnamed()
            return (
              <li
                key={passkey.id}
                className="flex items-center justify-between gap-3 rounded-lg border border-border bg-card px-3 py-2"
              >
                <div className="flex min-w-0 items-center gap-2">
                  <KeyRound className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span className="truncate text-sm font-medium">{label}</span>
                </div>
                {confirmingId === passkey.id ? (
                  <div className="flex shrink-0 items-center gap-1.5">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      onClick={() => setConfirmingId(null)}
                    >
                      {m.common_cancel()}
                    </Button>
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      disabled={deletingId === passkey.id}
                      onClick={() => void handleDelete(passkey.id)}
                    >
                      {m.auth_passkey_delete_confirm()}
                    </Button>
                  </div>
                ) : (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={m.auth_passkey_delete_aria({ name: label })}
                    onClick={() => setConfirmingId(passkey.id)}
                  >
                    <Trash2 aria-hidden="true" />
                  </Button>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
