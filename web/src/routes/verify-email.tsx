import { useState, type FormEvent } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button, buttonVariants } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { authClient } from '#/server/auth/client'

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

function VerifyEmailExpired() {
  const [email, setEmail] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [sent, setSent] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!email || !captchaToken) return

    setSubmitting(true)
    try {
      await authClient.sendVerificationEmail({
        email,
        callbackURL: '/verify-email?done=1',
        fetchOptions: { headers: { 'x-captcha-response': captchaToken } },
      })
      setSent(true)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthCard title={m.auth_verify_error_title()} subtitle={m.auth_verify_error_body()}>
      {sent ? (
        <p className="text-sm text-muted-foreground">{m.auth_verify_resend_success()}</p>
      ) : (
        <form onSubmit={(e) => void handleSubmit(e)} noValidate className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="verify-email">{m.auth_email_label()}</Label>
            <Input
              id="verify-email"
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </div>
          <TurnstileField onToken={setCaptchaToken} />
          <Button type="submit" className="w-full" disabled={submitting || !captchaToken}>
            {submitting ? m.auth_verify_resend_submitting() : m.auth_verify_resend_submit()}
          </Button>
        </form>
      )}
    </AuthCard>
  )
}
