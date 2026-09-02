import { useState } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { Button, buttonVariants } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { requestEmailVerification } from '#/api/auth'
import { useSession } from '#/lib/use-session'

export const Route = createFileRoute('/verify-email')({
  // TanStack Router's default search parser turns a number-looking value like `?done=1` into a
  // JS number, not a string — accept either without transforming (a `z.coerce` here would change
  // the value's type and make the router re-stringify + redirect to canonicalize the URL).
  validateSearch: z.object({
    done: z.union([z.string(), z.number()]).optional(),
    error: z.string().optional(),
  }),
  component: VerifyEmailPage,
})

function VerifyEmailPage() {
  const { done } = Route.useSearch()

  if (String(done) === '1') {
    return (
      <AuthCard title={m.auth_verify_done_title()}>
        <p className="text-sm text-muted-foreground">{m.auth_verify_done_body()}</p>
        <Link to="/" className={cn(buttonVariants(), 'w-full')}>
          {m.auth_verify_done_cta()}
        </Link>
      </AuthCard>
    )
  }

  return <VerifyEmailExpired />
}

/**
 * Limen's `email-verifications` (resend) route is protected and takes no target address — it
 * always resends to the CALLER'S OWN account (`internal/auth/routes.txt`), unlike the old
 * better-auth flow, which could resend to any typed-in address while signed out. Without a
 * session there is nothing to resend to, so this now asks the visitor to sign in first instead of
 * collecting an email/captcha.
 */
function VerifyEmailExpired() {
  const session = useSession()
  const [submitting, setSubmitting] = useState(false)
  const [sent, setSent] = useState(false)

  async function handleResend() {
    setSubmitting(true)
    try {
      await requestEmailVerification()
      setSent(true)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthCard title={m.auth_verify_error_title()} subtitle={m.auth_verify_error_body()}>
      {sent ? (
        <p className="text-sm text-muted-foreground">{m.auth_verify_resend_success()}</p>
      ) : session ? (
        <Button
          type="button"
          className="w-full"
          disabled={submitting}
          onClick={() => void handleResend()}
        >
          {submitting ? m.auth_verify_resend_submitting() : m.auth_verify_resend_submit()}
        </Button>
      ) : (
        <Link to="/login" className={cn(buttonVariants(), 'w-full')}>
          {m.auth_login_link()}
        </Link>
      )}
    </AuthCard>
  )
}
