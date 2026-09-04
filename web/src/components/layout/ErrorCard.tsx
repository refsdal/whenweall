import { useEffect } from 'react'
import { Link } from '@tanstack/react-router'
import { RotateCcw, TriangleAlert } from 'lucide-react'
import { Button } from '#/components/ui/button'
import { errorDetailMessage } from '#/lib/errors'
import { m } from '#/lib/i18n'

/**
 * The root route's `errorComponent` — an uncaught render/loader error anywhere in the app.
 *
 * The detail line under `error_body` comes from `errorDetailMessage`'s allowlist of our own error
 * codes, never from `error.message`: an unexpected error's text is written by whatever threw it
 * (Postgres, an upstream API, the runtime), so rendering it hands a visitor server internals for
 * free. The full error still reaches the console, which is where it is actually useful.
 */
export function ErrorCard({ error, onRetry }: { error?: unknown; onRetry?: () => void }) {
  const message = errorDetailMessage(error)

  useEffect(() => {
    if (error !== undefined) console.error('[error-boundary]', error)
  }, [error])

  return (
    <div className="mx-auto flex w-full max-w-lg flex-col items-center gap-4 px-5 py-24 text-center">
      <span className="inline-flex size-12 items-center justify-center rounded-full bg-destructive/10 text-destructive">
        <TriangleAlert aria-hidden="true" className="size-5" />
      </span>
      <h1 className="display text-2xl">{m.error_title()}</h1>
      <p className="text-sm text-muted-foreground">{m.error_body()}</p>
      {message && <p className="max-w-sm truncate text-xs text-muted-foreground/70">{message}</p>}
      <div className="flex items-center gap-3">
        {onRetry && (
          <Button type="button" variant="outline" onClick={onRetry}>
            <RotateCcw aria-hidden="true" />
            {m.error_retry()}
          </Button>
        )}
        <Button asChild>
          <Link to="/">{m.error_home()}</Link>
        </Button>
      </div>
    </div>
  )
}
