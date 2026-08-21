import { Link } from '@tanstack/react-router'
import { SearchX } from 'lucide-react'
import { Button } from '#/components/ui/button'

/**
 * The friendly "there's nothing here" card. Used by the root route's `notFoundComponent` (any
 * unmatched URL) and by `/p/$id` (a poll that doesn't exist, or was deleted), each with their
 * own copy.
 */
export function NotFoundCard({
  title,
  body,
  ctaLabel,
}: {
  title: string
  body: string
  ctaLabel: string
}) {
  return (
    <div className="mx-auto flex w-full max-w-lg flex-col items-center gap-4 px-5 py-24 text-center">
      <span className="inline-flex size-12 items-center justify-center rounded-full bg-secondary text-muted-foreground">
        <SearchX aria-hidden="true" className="size-5" />
      </span>
      <h1 className="display text-2xl">{title}</h1>
      <p className="text-sm text-muted-foreground">{body}</p>
      <Button asChild variant="outline">
        <Link to="/">{ctaLabel}</Link>
      </Button>
    </div>
  )
}
