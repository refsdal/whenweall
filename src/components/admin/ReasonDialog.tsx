import { useState, type ReactNode } from 'react'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { m } from '#/lib/i18n'

/**
 * The confirmation every staff action goes through.
 *
 * The reason is mandatory — the confirm button stays disabled until one is typed — because it is
 * the only part of an audit row a machine cannot reconstruct afterwards. Who, what and when are
 * recorded regardless; *why* exists only if someone types it here.
 */
export function ReasonDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  destructive,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  confirmLabel: string
  destructive?: boolean
  onConfirm: (reason: string) => Promise<void> | void
}) {
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const trimmed = reason.trim()

  async function handleConfirm() {
    if (!trimmed) return
    setBusy(true)
    try {
      await onConfirm(trimmed)
      setReason('')
      onOpenChange(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor="admin-reason">{m.admin_reason_label()}</Label>
          <Input
            id="admin-reason"
            value={reason}
            autoFocus
            placeholder={m.admin_reason_placeholder()}
            onChange={(event) => setReason(event.target.value)}
          />
          <p className="text-xs text-muted-foreground">{m.admin_reason_help()}</p>
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {m.admin_cancel()}
          </Button>
          <Button
            type="button"
            variant={destructive ? 'destructive' : 'default'}
            disabled={!trimmed || busy}
            onClick={handleConfirm}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
