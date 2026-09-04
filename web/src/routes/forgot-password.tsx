import { useState, type FormEvent } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import { toast } from 'sonner'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button, buttonVariants } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { authErrorMessage, isCaptchaFailedError } from '#/lib/auth-errors'
import { useCaptchaEnabled } from '#/lib/captcha'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { requestPasswordReset } from '#/api/auth'

export const Route = createFileRoute('/forgot-password')({
  component: ForgotPasswordPage,
})

const emailSchema = z.email()

// Exported (rather than kept module-private, like most route components) purely so its captcha
// handling can be unit-tested directly — see this file's own test, and verify-email.tsx's
// VerifyWithToken for the same pattern.
export function ForgotPasswordPage() {
  const captchaEnabled = useCaptchaEnabled()
  const [email, setEmail] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [sent, setSent] = useState(false)
  // A Turnstile token is good for exactly one submit (`authCaptchaMiddleware` verifies AND
  // redeems it before Limen ever sees the request); the widget is remounted (via its key) after a
  // captcha failure so a retry gets a fresh token instead of failing captcha_failed forever. Same
  // pattern as BookingForm's own `attempt` state.
  const [attempt, setAttempt] = useState(0)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    if (!emailSchema.safeParse(email).success) {
      setError(m.auth_error_email_invalid())
      return
    }
    setError(null)
    if (captchaEnabled && !captchaToken) return

    setSubmitting(true)
    try {
      await requestPasswordReset(email, captchaToken)
      setSent(true)
    } catch (err) {
      // A captcha failure is a request-level problem, not a question about whether the address is
      // registered — the server's captcha check runs before Limen ever looks the email up
      // (authCaptchaMiddleware sits in front of the whole /passwords/request-reset route), so
      // surfacing it leaks nothing an attacker could use. Reset the burned token and let the user
      // retry with a fresh one instead of silently claiming success for a request that never went
      // through. Every other failure still shows the success state below — this endpoint must
      // never let an attacker learn which emails are registered.
      if (isCaptchaFailedError(err)) {
        toast.error(authErrorMessage(err))
        setCaptchaToken(null)
        setAttempt((value) => value + 1)
      } else {
        setSent(true)
      }
    } finally {
      setSubmitting(false)
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

        <TurnstileField key={attempt} onToken={setCaptchaToken} />

        <Button type="submit" className="w-full" disabled={submitting || (captchaEnabled && !captchaToken)}>
          {submitting ? m.auth_forgot_submitting() : m.auth_forgot_submit()}
        </Button>
      </form>
    </AuthCard>
  )
}
