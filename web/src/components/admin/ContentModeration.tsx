import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { lookupContent, setContentSuspended } from '#/api/admin'
import { ApiError } from '#/api/client'
import type { AdminContentItem } from '#/api/types'
import { ReasonDialog } from '#/components/admin/ReasonDialog'
import { Badge } from '#/components/ui/badge'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { m } from '#/lib/i18n'

function kindLabel(item: AdminContentItem): string {
  if (item.kind === 'booking-page') return m.admin_content_kind_booking_page()
  return item.pollType === 'signup' ? m.admin_content_kind_signup() : m.admin_content_kind_poll()
}

/**
 * Find one poll, sign-up sheet or booking page from an abuse report and suspend it or lift its
 * suspension (#48, docs/abuse-reports.md). Every change goes through `ReasonDialog`, because the
 * backend requires a reason and records it in the audit log; after one, the item is looked up
 * again so the card shows the state the server actually holds.
 */
export function ContentModeration() {
  const [query, setQuery] = useState('')
  const [item, setItem] = useState<AdminContentItem | null>(null)
  const [notFound, setNotFound] = useState(false)
  const [searching, setSearching] = useState(false)
  const [pending, setPending] = useState<'suspend' | 'unsuspend' | null>(null)

  async function find(q: string) {
    setSearching(true)
    try {
      const found = await lookupContent(q)
      setItem(found)
      setNotFound(found === null)
    } catch (error) {
      const message = error instanceof ApiError ? error.message : String(error)
      toast.error(m.admin_action_failed({ message }))
    } finally {
      setSearching(false)
    }
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const q = query.trim()
    if (q) void find(q)
  }

  async function run(action: 'suspend' | 'unsuspend', reason: string) {
    if (!item) return
    try {
      await setContentSuspended(item, action === 'suspend', reason)
      toast.success(
        action === 'suspend' ? m.admin_content_toast_suspended() : m.admin_content_toast_unsuspended(),
      )
    } catch (error) {
      const message = error instanceof ApiError ? error.message : String(error)
      toast.error(m.admin_action_failed({ message }))
    }
    await find(item.id)
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">{m.admin_content_intro()}</p>

      <form onSubmit={handleSubmit} className="flex flex-col gap-2 sm:flex-row sm:items-end">
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="admin-content-q">{m.admin_content_search_label()}</Label>
          <Input
            id="admin-content-q"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="https://…/p/… or id"
          />
        </div>
        <Button type="submit" disabled={searching || !query.trim()}>
          {m.admin_content_search()}
        </Button>
      </form>

      {notFound && <p className="text-sm text-muted-foreground">{m.admin_content_not_found()}</p>}

      {item && (
        <section className="flex flex-col gap-3 rounded-lg border p-4" aria-label={item.title}>
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{kindLabel(item)}</Badge>
            {item.suspendedAt && <Badge variant="destructive">{m.admin_content_badge_suspended()}</Badge>}
            {item.deletedAt && <Badge variant="outline">{m.admin_content_badge_deleted()}</Badge>}
          </div>
          <h2 className="text-lg font-semibold break-words">{item.title}</h2>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
            <dt className="text-muted-foreground">{m.admin_content_organization()}</dt>
            <dd className="break-words">{item.organizationName}</dd>
            <dt className="text-muted-foreground">{m.admin_content_creator()}</dt>
            <dd className="break-words">{item.creatorEmail ?? m.admin_content_creator_gone()}</dd>
            <dt className="text-muted-foreground">{m.admin_content_created()}</dt>
            <dd>{new Date(item.createdAt).toLocaleString()}</dd>
          </dl>
          <div className="flex flex-wrap gap-2">
            {item.suspendedAt ? (
              <Button type="button" variant="outline" onClick={() => setPending('unsuspend')}>
                {m.admin_content_unsuspend()}
              </Button>
            ) : (
              <Button type="button" variant="destructive" onClick={() => setPending('suspend')}>
                {m.admin_content_suspend()}
              </Button>
            )}
            <Button asChild variant="ghost">
              <a href={item.publicPath} target="_blank" rel="noreferrer">
                {m.admin_content_open()}
              </a>
            </Button>
          </div>
        </section>
      )}

      {pending && (
        <ReasonDialog
          key={pending}
          open
          onOpenChange={(open) => {
            if (!open) setPending(null)
          }}
          title={pending === 'suspend' ? m.admin_content_suspend_title() : m.admin_content_unsuspend_title()}
          description={
            pending === 'suspend'
              ? m.admin_content_suspend_description()
              : m.admin_content_unsuspend_description()
          }
          confirmLabel={pending === 'suspend' ? m.admin_content_suspend() : m.admin_content_unsuspend()}
          destructive={pending === 'suspend'}
          onConfirm={(reason) => run(pending, reason)}
        />
      )}
    </div>
  )
}
