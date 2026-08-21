import * as z from 'zod'

/**
 * Shared `next` search param: where to send the user after an auth flow completes. Restricted
 * to same-origin paths (`startsWith('/')`) so a crafted `?next=` can't redirect off-site.
 */
export const nextSearchSchema = z.object({
  next: z.string().startsWith('/').optional(),
})

export type NextSearch = z.infer<typeof nextSearchSchema>
