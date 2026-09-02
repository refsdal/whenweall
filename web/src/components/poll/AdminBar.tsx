import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useServerFn } from '@tanstack/react-start'
import {
  Bell,
  Copy,
  Crown,
  Download,
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
import { NotificationGrid } from '#/components/notifications/NotificationGrid'
import {
  POLL_NOTIFICATION_EVENTS,
  type NotificationGrid as NotificationGridValue,
} from '#/lib/notifications'
import type { AppLocale } from '#/app.config'
import { m } from '#/lib/i18n'
import {
  deletePoll,
  duplicatePoll,
  setPollFollowing,
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
  pushAvailable = false,
}: {
  poll: PollView
  onChanged: () => void | Promise<void>
  onShare: () => void
  locale: AppLocale
  timeZone: string
  /** From the viewer's entitlements — the push column is Premium-only. */
  pushAvailable?: boolean
}) {
  const navigate = useNavigate()
  const statusFn = useServerFn(setPollStatus)
  const duplicateFn = useServerFn(duplicatePoll)
  const deleteFn = useServerFn(deletePoll)
  const prefsFn = useServerFn(updateNotificationPrefs)
  const followFn = useServerFn(setPollFollowing)
  const defaultChannels = poll.notifications?.defaults ?? null

  const [finalizeOpen, setFinalizeOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [channels, setChannels] = useState<NotificationGridValue | null>(
    poll.notifications?.channels ?? null,
  )
  const [following, setFollowing] = useState(poll.notifications?.following ?? false)

  const isClosed = poll.status !== 'open'
  // A sign-up sheet has no winning option to pick — the organiser closes it instead, and takes the
  // roster with them.
  const isSignup = poll.type === 'signup'

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

  async function savePrefs(next: NotificationGridValue | null) {
    const previous = channels
    setChannels(next)
    // Tuning a poll you were not following is an implicit follow, server-side — mirror that here
    // so the toggle does not appear to disagree with the checkboxes.
    setFollowing(true)
    try {
      await prefsFn({ data: { pollId: poll.id, channels: next } })
      toast.success(m.poll_notify_saved())
    } catch {
      setChannels(previous)
      toast.error(m.poll_error_generic())
    }
  }

  async function toggleFollowing(next: boolean) {
    const previous = following
    setFollowing(next)
    try {
      await followFn({ data: { pollId: poll.id, following: next } })
    } catch {
      setFollowing(previous)
      toast.error(m.poll_error_generic())
    }
  }

  return (
    <div
      data-testid="admin-bar"
      // Pinned to the bottom of a phone viewport, which on a handset with a home indicator means
      // the last 34px of it belong to the system gesture strip, not to us.
      className="sticky bottom-0 z-30 -mx-5 border-t border-border bg-card/95 px-5 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur supports-[backdrop-filter]:bg-card/80 sm:static sm:mx-0 sm:rounded-xl sm:border sm:px-4 sm:py-3 sm:shadow-sm sm:backdrop-blur-none"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="hidden text-xs font-medium tracking-wide text-muted-foreground uppercase sm:mr-1 sm:inline">
          {m.poll_admin_title()}
        </span>

        {isSignup ? (
          <Button asChild type="button" size="sm" variant="outline">
            {/* A plain link, not a server function: the CSV comes straight from an owner-only
                route, so the browser can stream it to disk. */}
            <a href={`/p/${poll.id}/roster.csv`} download>
              <Download aria-hidden="true" />
              {m.admin_download_roster()}
            </a>
          </Button>
        ) : (
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
        )}

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
          <PopoverContent align="end" className="w-88">
            <PopoverHeader>
              <PopoverTitle>{m.poll_notifications()}</PopoverTitle>
            </PopoverHeader>
            <div className="mt-3 flex flex-col gap-3">
              <label className="flex cursor-pointer items-center justify-between gap-3 text-sm">
                {m.notif_following()}
                <Switch
                  checked={following}
                  onCheckedChange={(checked) => void toggleFollowing(checked)}
                />
              </label>

              <NotificationGrid
                events={POLL_NOTIFICATION_EVENTS}
                value={channels}
                defaults={defaultChannels}
                pushAvailable={pushAvailable}
                disabled={!following}
                onChange={(next) => void savePrefs(next)}
              />

              {channels === null ? (
                <p className="text-xs text-muted-foreground">{m.notif_using_defaults()}</p>
              ) : (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => void savePrefs(null)}
                  className="self-start"
                >
                  {m.notif_reset_to_defaults()}
                </Button>
              )}
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
            <DropdownMenuItem asChild>
              <Link to="/p/$id/edit" params={{ id: poll.id }}>
                <Pencil aria-hidden="true" />
                {m.poll_edit()}
              </Link>
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

      {!isSignup && (
        <FinalizeDialog
          poll={poll}
          open={finalizeOpen}
          onOpenChange={setFinalizeOpen}
          onFinalized={onChanged}
          locale={locale}
          timeZone={timeZone}
        />
      )}

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
