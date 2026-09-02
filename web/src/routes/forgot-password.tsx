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

export const Route = createFileRoute('/forgot-password')({
  component: ForgotPasswordPage,
})

const emailSchema = z.email()

function ForgotPasswordPage() {
  const [email, setEmail] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [sent, setSent] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    if (!emailSchema.safeParse(email).success) {
      setError(m.auth_error_email_invalid())
      return
    }
    setError(null)
    if (!captchaToken) return

    setSubmitting(true)
    try {
      await authClient.requestPasswordReset({
        email,
        redirectTo: '/reset-password',
        fetchOptions: { headers: { 'x-captcha-response': captchaToken } },
      })
    } finally {
      // Always show the success state, whether or not the address has an account — the request
      // endpoint must not let an attacker learn which emails are registered.
      setSubmitting(false)
      setSent(true)
    }
  }

  if (sent) {
    return (
      <AuthCard title={m.auth_forgot_success_title()}>
        <p className="text-sm text-muted-foreground">{m.auth_forgot_success_body({ email })}</p>
        <Link to="/login" className={cn(buttonVariants(), 'w-full')}>
          {m.auth_forgot_back_to_login()}
        </Link>
      </AuthCard>
    )
  }

  return (
    <AuthCard title={m.auth_forgot_title()} subtitle={m.auth_forgot_subtitle()}>
      <form onSubmit={(e) => void handleSubmit(e)} noValidate className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="forgot-email">{m.auth_email_label()}</Label>
          <Input
            id="forgot-email"
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-invalid={!!error}
            aria-describedby={error ? 'forgot-email-error' : undefined}
          />
          {error && (
            <p id="forgot-email-error" className="text-sm text-destructive">
              {error}
            </p>
          )}
        </div>

        <TurnstileField onToken={setCaptchaToken} />

        <Button type="submit" className="w-full" disabled={submitting || !captchaToken}>
          {submitting ? m.auth_forgot_submitting() : m.auth_forgot_submit()}
        </Button>
      </form>
    </AuthCard>
  )
}
