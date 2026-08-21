import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useServerFn } from '@tanstack/react-start'
import {
  Bell,
  Copy,
  Crown,
  Lock,
  LockOpen,
  MoreHorizontal,
  Pencil,
  Share2,
  Trash2,
} from 'lucide-react'
import { toast } from 'sonner'
import { FinalizeDialog } from '#/components/poll/FinalizeDialog'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '#/components/ui/dropdown-menu'
import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '#/components/ui/popover'
import { Switch } from '#/components/ui/switch'
import type { AppLocale } from '#/app.config'
import { m } from '#/lib/i18n'
import {
  deletePoll,
  duplicatePoll,
  setPollStatus,
  updateNotificationPrefs,
} from '#/server/polls/polls.functions'
import type { PollView } from '#/server/polls/viewmodel'

/**
 * The organiser's toolbar. A sticky bar at the bottom of the viewport on a phone (where it sits
 * under the thumb) and a card above the grid on a desktop. Destructive actions are one dialog
 * away; the rest are one tap.
 */
export function AdminBar({
  poll,
  onChanged,
  onShare,
  locale,
  timeZone,
}: {
  poll: PollView
  onChanged: () => void | Promise<void>
  onShare: () => void
  locale: AppLocale
  timeZone: string
}) {
  const navigate = useNavigate()
  const statusFn = useServerFn(setPollStatus)
  const duplicateFn = useServerFn(duplicatePoll)
  const deleteFn = useServerFn(deletePoll)
  const prefsFn = useServerFn(updateNotificationPrefs)

  const [finalizeOpen, setFinalizeOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [prefs, setPrefs] = useState({
    notifyOnVote: poll.notifications?.notifyOnVote ?? true,
    notifyOnComment: poll.notifications?.notifyOnComment ?? true,
  })

  const isClosed = poll.status !== 'open'

  async function run(action: () => Promise<void>) {
    setBusy(true)
    try {
      await action()
    } catch {
      toast.error(m.poll_error_generic())
    } finally {
      setBusy(false)
    }
  }

  async function toggleStatus() {
    await run(async () => {
      const status = poll.status === 'open' ? 'closed' : 'open'
      await statusFn({ data: { pollId: poll.id, status } })
      toast.success(status === 'closed' ? m.poll_closed_toast() : m.poll_reopened_toast())
      await onChanged()
    })
  }

  async function duplicate() {
    await run(async () => {
      const { id } = await duplicateFn({ data: { pollId: poll.id } })
      toast.success(m.poll_duplicated())
      await navigate({ href: `/p/${id}` })
    })
  }

  async function remove() {
    await run(async () => {
      await deleteFn({ data: { pollId: poll.id } })
      setDeleteOpen(false)
      toast.success(m.poll_deleted())
      await navigate({ to: '/' })
    })
  }

  async function savePrefs(next: { notifyOnVote: boolean; notifyOnComment: boolean }) {
    setPrefs(next)
    try {
      await prefsFn({ data: { pollId: poll.id, ...next } })
      toast.success(m.poll_notify_saved())
    } catch {
      setPrefs(prefs)
      toast.error(m.poll_error_generic())
    }
  }

  return (
    <div
      data-testid="admin-bar"
      className="sticky bottom-0 z-30 -mx-5 border-t border-border bg-card/95 px-5 py-3 backdrop-blur supports-[backdrop-filter]:bg-card/80 sm:static sm:mx-0 sm:rounded-xl sm:border sm:px-4 sm:shadow-sm sm:backdrop-blur-none"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="hidden text-xs font-medium tracking-wide text-muted-foreground uppercase sm:mr-1 sm:inline">
          {m.poll_admin_title()}
        </span>

        <Button
          type="button"
          size="sm"
          // Re-finalizing is a conflict on the server, so a decided poll offers no pick button.
          disabled={busy || poll.options.length === 0 || poll.status === 'finalized'}
          onClick={() => setFinalizeOpen(true)}
        >
          <Crown aria-hidden="true" />
          {m.poll_finalize()}
        </Button>

        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy || poll.status === 'finalized'}
          onClick={() => void toggleStatus()}
        >
          {isClosed ? <LockOpen aria-hidden="true" /> : <Lock aria-hidden="true" />}
          <span className="hidden sm:inline">
            {isClosed ? m.poll_reopen_voting() : m.poll_close_voting()}
          </span>
        </Button>

        <Button type="button" size="sm" variant="outline" onClick={onShare}>
          <Share2 aria-hidden="true" />
          <span className="hidden sm:inline">{m.poll_share()}</span>
        </Button>

        <Popover>
          <PopoverTrigger asChild>
            <Button
              type="button"
              size="icon-sm"
              variant="ghost"
              aria-label={m.poll_notifications()}
            >
              <Bell aria-hidden="true" />
            </Button>
          </PopoverTrigger>
          <PopoverContent align="end" className="w-72">
            <PopoverHeader>
              <PopoverTitle>{m.poll_notifications()}</PopoverTitle>
            </PopoverHeader>
            <div className="mt-3 flex flex-col gap-3">
              <label className="flex cursor-pointer items-center justify-between gap-3 text-sm">
                {m.poll_notify_votes()}
                <Switch
                  checked={prefs.notifyOnVote}
                  onCheckedChange={(checked) => void savePrefs({ ...prefs, notifyOnVote: checked })}
                />
              </label>
              <label className="flex cursor-pointer items-center justify-between gap-3 text-sm">
                {m.poll_notify_comments()}
                <Switch
                  checked={prefs.notifyOnComment}
                  onCheckedChange={(checked) =>
                    void savePrefs({ ...prefs, notifyOnComment: checked })
                  }
                />
              </label>
            </div>
          </PopoverContent>
        </Popover>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              size="icon-sm"
              variant="ghost"
              aria-label={m.poll_more_actions()}
              className="ml-auto sm:ml-0"
            >
              <MoreHorizontal aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {/* The edit page is a separate route; a plain link keeps this working before it ships. */}
            <DropdownMenuItem asChild>
              <a href={`/p/${poll.id}/edit`}>
                <Pencil aria-hidden="true" />
                {m.poll_edit()}
              </a>
            </DropdownMenuItem>
            <DropdownMenuItem disabled={busy} onSelect={() => void duplicate()}>
              <Copy aria-hidden="true" />
              {m.poll_duplicate()}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={() => setDeleteOpen(true)}>
              <Trash2 aria-hidden="true" />
              {m.poll_delete()}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <FinalizeDialog
        poll={poll}
        open={finalizeOpen}
        onOpenChange={setFinalizeOpen}
        onFinalized={onChanged}
        locale={locale}
        timeZone={timeZone}
      />

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{m.poll_delete_title()}</DialogTitle>
            <DialogDescription>{m.poll_delete_body()}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setDeleteOpen(false)}>
              {m.common_cancel()}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={busy}
              onClick={() => void remove()}
            >
              {m.poll_delete_confirm()}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
