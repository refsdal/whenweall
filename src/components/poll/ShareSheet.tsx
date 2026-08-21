import { useEffect, useState, useSyncExternalStore } from 'react'
import { Check, Copy, Share2 } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { m } from '#/lib/i18n'

const noopSubscribe = () => () => {}

function hasNativeShare(): boolean {
  return typeof navigator !== 'undefined' && typeof navigator.share === 'function'
}

/** The share dialog: one link, copied in one tap, plus the OS share sheet where there is one. */
export function ShareSheet({
  url,
  open,
  onOpenChange,
}: {
  url: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [copied, setCopied] = useState(false)
  // Feature detection has to wait for the browser: `navigator.share` doesn't exist on the server,
  // and rendering the button during SSR would break hydration.
  const canShare = useSyncExternalStore(noopSubscribe, hasNativeShare, () => false)

  useEffect(() => {
    if (!copied) return
    const id = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(id)
  }, [copied])

  async function copy() {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      toast.success(m.poll_share_copied())
    } catch {
      toast.error(m.poll_share_copy_failed())
    }
  }

  async function share() {
    try {
      await navigator.share({ url })
    } catch {
      // The visitor dismissed the OS sheet, or it refused — nothing to report.
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="display text-xl">{m.poll_share_title()}</DialogTitle>
          <DialogDescription>{m.poll_share_desc()}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor="share-url">{m.poll_share_link_label()}</Label>
          <Input
            id="share-url"
            readOnly
            value={url}
            onFocus={(event) => event.currentTarget.select()}
            className="font-mono text-xs sm:text-sm"
          />
        </div>

        <div className="flex flex-col gap-2 sm:flex-row">
          <Button type="button" onClick={() => void copy()} className="flex-1">
            {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
            {m.poll_share_copy()}
          </Button>
          {canShare && (
            <Button type="button" variant="outline" onClick={() => void share()} className="flex-1">
              <Share2 aria-hidden="true" />
              {m.poll_share_native()}
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
