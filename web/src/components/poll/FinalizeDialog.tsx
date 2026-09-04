import { useState } from 'react'
import { Crown } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import type { AppLocale } from '#/app.config'
import { celebrate } from '#/lib/confetti'
import { m } from '#/lib/i18n'
import { formatOptionLabel } from '#/lib/time'
import { cn } from '#/lib/utils'
import { finalizePoll } from '#/api/polls'
import type { PollView } from '#/api/types'

/** The organiser's last click: pick the winning option and tell everyone who left an email. */
export function FinalizeDialog({
  poll,
  open,
  onOpenChange,
  onFinalized,
  locale,
  timeZone,
}: {
  poll: PollView
  open: boolean
  onOpenChange: (open: boolean) => void
  onFinalized: () => void | Promise<void>
  locale: AppLocale
  timeZone: string
}) {
  const preselected = poll.finalizedOptionId ?? poll.bestOptionId ?? poll.options[0]?.id ?? ''
  const [selected, setSelected] = useState(preselected)
  const [wasOpen, setWasOpen] = useState(open)
  const [submitting, setSubmitting] = useState(false)

  // The tally moves while the dialog is closed, so the pre-selection is refreshed every time it
  // opens rather than only on first mount. Adjusting state during render (rather than in an
  // effect) keeps it to a single render pass.
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setSelected(preselected)
  }

  async function confirm() {
    if (!selected) return
    setSubmitting(true)
    try {
      const { sent } = await finalizePoll(poll.id, selected)
      celebrate('finalize')
      toast.success(m.poll_finalized_emails({ count: sent }))
      onOpenChange(false)
      await onFinalized()
    } catch {
      toast.error(m.poll_error_generic())
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="display text-xl">{m.poll_finalize_title()}</DialogTitle>
          <DialogDescription>{m.poll_finalize_desc()}</DialogDescription>
        </DialogHeader>

        <fieldset className="-mx-1 max-h-[50vh] overflow-y-auto px-1">
          <legend className="sr-only">{m.poll_finalize_title()}</legend>
          <div className="flex flex-col gap-1.5">
            {poll.options.map((option) => {
              const label = formatOptionLabel(option, { locale, timeZone })
              const score = poll.scores[option.id] ?? { yes: 0, ifneedbe: 0, no: 0, score: 0 }
              const isBest = option.id === poll.bestOptionId
              const isSelected = option.id === selected

              return (
                <label
                  key={option.id}
                  className={cn(
                    'flex cursor-pointer items-center gap-3 rounded-xl border px-3 py-2.5 transition-colors',
                    isSelected
                      ? 'border-[var(--primary)] bg-accent-soft'
                      : 'border-border hover:bg-secondary',
                  )}
                >
                  <input
                    type="radio"
                    name="finalize-option"
                    value={option.id}
                    checked={isSelected}
                    onChange={() => setSelected(option.id)}
                    className="size-4 accent-[var(--primary-strong)]"
                  />
                  <span className="min-w-0 flex-1 text-sm" suppressHydrationWarning>
                    {[label.primary, label.secondary, label.tertiary].filter(Boolean).join(' ')}
                  </span>
                  {isBest && (
                    <Crown aria-hidden="true" className="size-3.5 text-[var(--primary-ink)]" />
                  )}
                  <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                    {m.poll_score_yes({ count: score.yes })}
                    {score.ifneedbe > 0 ? ` · +${score.ifneedbe}` : ''}
                  </span>
                </label>
              )
            })}
          </div>
        </fieldset>

        <DialogFooter>
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            {m.common_cancel()}
          </Button>
          <Button type="button" disabled={submitting || !selected} onClick={() => void confirm()}>
            {submitting ? m.poll_finalizing() : m.poll_finalize_confirm()}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
