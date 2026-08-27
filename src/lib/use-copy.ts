import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'

const RESET_MS = 2000

/**
 * Copy-to-clipboard with the "copied!" state that follows it.
 *
 * Three places in the product hand out a link, each with its own button shape, so this carries
 * only the part they agree on: write the text, confirm it, and fall back to the state the button
 * started in a couple of seconds later. The wording differs per site and is passed in.
 */
export function useCopy(
  messages: { success: string; error: string },
  resetMs = RESET_MS,
): { copied: boolean; copy: (text: string) => Promise<void> } {
  const { success, error } = messages
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(false), resetMs)
    return () => clearTimeout(timer)
  }, [copied, resetMs])

  const copy = useCallback(
    async (text: string) => {
      try {
        await navigator.clipboard.writeText(text)
        setCopied(true)
        toast.success(success)
      } catch {
        // Denied permission, an insecure origin, or a browser without the API — all the same to
        // the visitor, who still has the link selectable in the field next to the button.
        toast.error(error)
      }
    },
    [success, error],
  )

  return { copied, copy }
}
