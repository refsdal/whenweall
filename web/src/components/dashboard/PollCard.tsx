import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  CalendarDays,
  ClipboardList,
  Copy,
  ListChecks,
  Trash2,
  Users,
  type LucideIcon,
} from 'lucide-react'
import { DeadlineCountdown } from '#/components/poll/DeadlineCountdown'
import { Badge } from '#/components/ui/badge'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import type { PollStatus, PollType } from '#/server/db/schema'
import type { PollSummary } from '#/server/polls/viewmodel'

const TYPE_ICON: Record<PollType, LucideIcon> = {
  datetime: CalendarDays,
  options: ListChecks,
  signup: ClipboardList,
}

/** Status pill colours borrow the vote-answer palette: open reads as "go", closed as "pending",
 * finalized gets the same accent ring the winning option uses on the poll page. */
const STATUS_CLASS: Record<PollStatus, string> = {
  open: 'bg-yes-soft text-yes-ink',
  closed: 'bg-ifneedbe-soft text-ifneedbe-ink',
  finalized: 'bg-accent-soft text-accent-foreground ring-1 ring-[var(--best)]',
}

function statusLabel(status: PollStatus): string {
  if (status === 'open') return m.poll_status_open()
  if (status === 'closed') return m.poll_status_closed()
  return m.poll_status_finalized()
}

/** A sign-up sheet counts sign-ups, every other poll counts people who answered. */
function participantsLabel(type: PollType, count: number): string {
  if (type === 'signup') {
    return count === 1
      ? m.dashboard_signups_count_one()
      : m.dashboard_signups_count_other({ count })
  }
  return count === 1 ? m.poll_meta_people_one() : m.poll_meta_people_other({ count })
}

export function PollCard({
  poll,
  onDuplicate,
  onDelete,
}: {
  poll: PollSummary
  onDuplicate: () => void | Promise<void>
  onDelete: () => void | Promise<void>
}) {
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const Icon = TYPE_ICON[poll.type]

  async function handleDelete() {
    setBusy(true)
    try {
      await onDelete()
      setDeleteOpen(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div data-testid="poll-card" className="surface flex flex-col gap-3 p-4">
      <div className="flex items-start justify-between gap-2">
        <Link
          to="/p/$id"
          params={{ id: poll.id }}
          className="focus-ring flex min-w-0 items-start gap-2.5 rounded-md"
        >
          <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
            <Icon aria-hidden="true" className="size-4" />
          </span>
          <span className="min-w-0 truncate font-medium hover:underline">{poll.title}</span>
        </Link>
        <Badge data-testid="poll-card-status" className={cn('shrink-0', STATUS_CLASS[poll.status])}>
          {statusLabel(poll.status)}
        </Badge>
      </div>

      <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <Users aria-hidden="true" className="size-3.5" />
          {participantsLabel(
            poll.type,
            poll.type === 'signup' ? poll.claimCount : poll.participantCount,
          )}
        </span>
        {poll.deadlineAt ? (
          <DeadlineCountdown deadlineAt={poll.deadlineAt} />
        ) : (
          <span>{m.creator_deadline_none()}</span>
        )}
      </div>

      <div className="mt-1 flex flex-wrap items-center gap-1.5">
        <Button asChild size="sm" variant="outline">
          <Link to="/p/$id" params={{ id: poll.id }}>
            {m.dashboard_open_action()}
          </Link>
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={() => void onDuplicate()}>
          <Copy aria-hidden="true" />
          {m.poll_duplicate()}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          aria-label={m.poll_delete()}
          className="ml-auto text-destructive hover:bg-destructive/10 hover:text-destructive"
          onClick={() => setDeleteOpen(true)}
        >
          <Trash2 aria-hidden="true" />
          <span className="hidden sm:inline">{m.poll_delete()}</span>
        </Button>
      </div>

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
              onClick={() => void handleDelete()}
            >
              {m.poll_delete_confirm()}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
