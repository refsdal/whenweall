import { useState } from 'react'
import { toast } from 'sonner'
import { deleteAdminUser, lockUser, unlockUser } from '#/api/admin'
import { ApiError } from '#/api/client'
import type { AdminUserDetail } from '#/api/types'
import { ReasonDialog } from '#/components/admin/ReasonDialog'
import { Button } from '#/components/ui/button'
import { m } from '#/lib/i18n'

type Action = 'lock' | 'unlock' | 'delete'

function copyFor(action: Action): { title: string; description: string; confirmLabel: string } {
  switch (action) {
    case 'lock':
      return {
        title: m.admin_lock_title(),
        description: m.admin_lock_description(),
        confirmLabel: m.admin_confirm_lock(),
      }
    case 'unlock':
      return {
        title: m.admin_unlock_title(),
        description: m.admin_unlock_description(),
        confirmLabel: m.admin_confirm_unlock(),
      }
    case 'delete':
      return {
        title: m.admin_delete_title(),
        description: m.admin_delete_description(),
        confirmLabel: m.admin_confirm_delete(),
      }
  }
}

/**
 * Lock / unlock / delete for one account — the console's only mutating controls — each behind
 * `ReasonDialog`, because the backend (internal/admin/handlers.go) requires a non-blank reason
 * and records it in the audit log. The same backend rejects a staff member targeting themselves
 * with 400; `isSelf` hides the controls up front so that 400 is never how anyone finds out.
 *
 * Lock and unlock leave the row in place, so `onChanged` lets the page refetch it; delete does
 * not, so `onDeleted` lets the page navigate away instead of reloading a 404.
 */
export function UserActions({
  user,
  isSelf,
  onChanged,
  onDeleted,
}: {
  user: AdminUserDetail
  isSelf: boolean
  onChanged: () => Promise<void> | void
  onDeleted: () => Promise<void> | void
}) {
  const [pending, setPending] = useState<Action | null>(null)

  async function run(action: Action, reason: string) {
    try {
      if (action === 'lock') {
        await lockUser(user.id, reason)
        toast.success(m.admin_toast_locked())
        await onChanged()
      } else if (action === 'unlock') {
        await unlockUser(user.id, reason)
        toast.success(m.admin_toast_unlocked())
        await onChanged()
      } else {
        await deleteAdminUser(user.id, reason)
        toast.success(m.admin_toast_deleted())
        await onDeleted()
      }
    } catch (error) {
      const message = error instanceof ApiError ? error.message : String(error)
      toast.error(m.admin_action_failed({ message }))
    }
  }

  if (isSelf) {
    return <p className="text-sm text-muted-foreground">{m.admin_action_self()}</p>
  }

  const copy = pending ? copyFor(pending) : null

  return (
    <div className="flex flex-wrap gap-2">
      {user.locked ? (
        <Button type="button" variant="outline" onClick={() => setPending('unlock')}>
          {m.admin_action_unlock()}
        </Button>
      ) : (
        <Button type="button" variant="outline" onClick={() => setPending('lock')}>
          {m.admin_action_lock()}
        </Button>
      )}
      <Button type="button" variant="destructive" onClick={() => setPending('delete')}>
        {m.admin_action_delete()}
      </Button>

      {pending && copy && (
        <ReasonDialog
          key={pending}
          open
          onOpenChange={(open) => {
            if (!open) setPending(null)
          }}
          title={copy.title}
          description={copy.description}
          confirmLabel={copy.confirmLabel}
          destructive={pending === 'delete'}
          onConfirm={(reason) => run(pending, reason)}
        />
      )}
    </div>
  )
}
