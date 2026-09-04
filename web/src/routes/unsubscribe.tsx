import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { Button, buttonVariants } from '#/components/ui/button'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { resubscribe, unsubscribe } from '#/api/unsubscribe'

export const Route = createFileRoute('/unsubscribe')({
  // `token` is what the footer link and the List-Unsubscribe header carry — an HMAC over the
  // recipient's address (internal/mailer.UnsubscribeToken). Optional in the schema so a
  // hand-trimmed URL renders the "not valid" card instead of throwing a search-params error.
  validateSearch: z.object({ token: z.string().optional() }),
  component: UnsubscribePage,
})

function UnsubscribePage() {
  const { token } = Route.useSearch()
  return <UnsubscribePanel token={token} />
}

/**
 * The confirmation page behind every notification email's unsubscribe link.
 *
 * Nothing happens on load, only on a click. The link is fetched by things that are not people —
 * corporate link scanners, spam filters, chat previews — and an unsubscribe that fires on GET
 * would silence recipients who never chose anything. (The server enforces the same rule: a GET on
 * the API path redirects here rather than acting.)
 *
 * Resubscribe is on the same page, reachable with the same token, because this link is the only
 * credential a guest recipient has. Without it one accidental click would be permanent, and
 * "as easy to withdraw as to give" has to run in both directions to be worth anything.
 *
 * Exported for `unsubscribe.test.tsx`, which drives the states directly rather than through the
 * route tree.
 */
export function UnsubscribePanel({ token }: { token?: string }) {
  const [state, setState] = useState<'idle' | 'busy' | 'done' | 'undone' | 'invalid' | 'error'>(
    token ? 'idle' : 'invalid',
  )
  const [email, setEmail] = useState('')

  async function run(action: (token: string) => Promise<{ email: string }>, next: 'done' | 'undone') {
    if (!token) return
    setState('busy')
    try {
      const result = await action(token)
      setEmail(result.email)
      setState(next)
    } catch (error) {
      // A rejected token is the one failure worth its own card: it means the link was altered or
      // the secret rotated, and retrying will never help.
      setState(errorCode(error) === 'invalid_token' ? 'invalid' : 'error')
    }
  }

  if (state === 'invalid') {
    return (
      <AuthCard title={m.unsub_invalid_title()} subtitle={m.unsub_invalid_body()}>
        <Link to="/" className={cn(buttonVariants({ variant: 'outline' }), 'w-full')}>
          {m.error_home()}
        </Link>
      </AuthCard>
    )
  }

  if (state === 'done') {
    return (
      <AuthCard title={m.unsub_done_title()} subtitle={m.unsub_done_body({ email })}>
        <Button type="button" variant="outline" className="w-full" onClick={() => void run(resubscribe, 'undone')}>
          {m.unsub_undo()}
        </Button>
      </AuthCard>
    )
  }

  if (state === 'undone') {
    return (
      <AuthCard title={m.unsub_undone_title()} subtitle={m.unsub_undone_body({ email })}>
        <Link to="/" className={cn(buttonVariants({ variant: 'outline' }), 'w-full')}>
          {m.error_home()}
        </Link>
      </AuthCard>
    )
  }

  return (
    <AuthCard title={m.unsub_title()} subtitle={m.unsub_body()}>
      <Button
        type="button"
        className="w-full"
        disabled={state === 'busy'}
        onClick={() => void run(unsubscribe, 'done')}
      >
        {state === 'busy' ? m.unsub_submitting() : m.unsub_submit()}
      </Button>
      {state === 'error' ? (
        <p className="text-center text-sm text-destructive" role="alert">
          {m.unsub_error()}
        </p>
      ) : null}
    </AuthCard>
  )
}
