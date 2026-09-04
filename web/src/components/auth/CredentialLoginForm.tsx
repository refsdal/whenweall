import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import * as z from 'zod'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { authErrorMessage } from '#/lib/auth-errors'
import { useCaptchaEnabled } from '#/lib/captcha'
import { m } from '#/lib/i18n'
import { me, requestEmailVerification, signInWithCredential, signOut, type AuthUser } from '#/api/auth'

const emailSchema = z.email()

type Outcome = { kind: 'form' } | { kind: 'unverified' } | { kind: 'locked' }

/**
 * The email+password form on /login, router-free so it can be unit-tested. After Limen accepts
 * the credentials this re-reads `me()` to learn what kind of session it got:
 *
 *   - `null`: the session exists but internal/auth's AuthMountGuard refuses it — the account is
 *     locked (the only way a fresh sign-in yields no `/me`). Sign out again (allowed for a locked
 *     session) and say so.
 *   - `emailVerified === false`: the old Better-Auth 403 branch, re-expressed — show the
 *     unverified card with a resend button (the session is what authorizes the resend) rather
 *     than continuing into an app that would refuse every request.
 *   - otherwise: `onSignedIn(user)`; the route decides where to go.
 */
export function CredentialLoginForm({ onSignedIn }: { onSignedIn: (user: AuthUser) => void | Promise<void> }) {
  const captchaEnabled = useCaptchaEnabled()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [errors, setErrors] = useState<{ email?: string; password?: string }>({})
  const [submitting, setSubmitting] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>({ kind: 'form' })
  const [resending, setResending] = useState(false)
  // A Turnstile token is good for exactly one submit (`authCaptchaMiddleware` verifies AND
  // redeems it before Limen ever sees the request), so a rejected sign-in — wrong password, a
  // locked/unverified outcome, anything — still burns it; the widget is remounted (via its key)
  // after every failed attempt so a retry gets a fresh token instead of failing captcha_failed
  // forever. Same pattern as BookingForm's own `attempt` state.
  const [attempt, setAttempt] = useState(0)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    const fieldErrors: typeof errors = {}
    if (!emailSchema.safeParse(email).success) fieldErrors.email = m.auth_error_email_invalid()
    if (password.length === 0) fieldErrors.password = m.auth_error_password_required()
    setErrors(fieldErrors)
    if (Object.keys(fieldErrors).length > 0) return
    if (captchaEnabled && !captchaToken) {
      toast.error(m.auth_error_captcha_required())
      return
    }

    setSubmitting(true)
    try {
      await signInWithCredential(email, password, captchaToken)
      const user = await me()
      if (!user) {
        await signOut()
        setOutcome({ kind: 'locked' })
        return
      }
      if (!user.emailVerified) {
        setOutcome({ kind: 'unverified' })
        return
      }
      await onSignedIn(user)
    } catch (error) {
      toast.error(authErrorMessage(error))
      setCaptchaToken(null)
      setAttempt((value) => value + 1)
    } finally {
      setSubmitting(false)
    }
  }

  async function handleResend() {
    setResending(true)
    try {
      await requestEmailVerification()
      toast.success(m.auth_verify_resent())
    } catch (error) {
      toast.error(authErrorMessage(error))
    } finally {
      setResending(false)
    }
  }

  if (outcome.kind === 'unverified') {
    return (
      <div className="flex flex-col gap-3 rounded-lg border border-border bg-secondary/60 p-4 text-sm" role="status">
        <p className="font-medium">{m.auth_login_unverified()}</p>
        <p className="text-muted-foreground">{m.auth_login_unverified_hint()}</p>
        <Button type="button" variant="outline" size="sm" disabled={resending} onClick={() => void handleResend()}>
          {m.auth_resend_verification()}
        </Button>
      </div>
    )
  }

  if (outcome.kind === 'locked') {
    return (
      <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm" role="alert">
        <p>{m.auth_login_locked()}</p>
      </div>
    )
  }

  return (
    <form onSubmit={(e) => void handleSubmit(e)} noValidate className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="login-email">{m.auth_email_label()}</Label>
        <Input
          id="login-email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          aria-invalid={!!errors.email}
          aria-describedby={errors.email ? 'login-email-error' : undefined}
        />
        {errors.email && (
          <p id="login-email-error" className="text-sm text-destructive">
            {errors.email}
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="login-password">{m.auth_password_label()}</Label>
        <Input
          id="login-password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-invalid={!!errors.password}
          aria-describedby={errors.password ? 'login-password-error' : undefined}
        />
        {errors.password && (
          <p id="login-password-error" className="text-sm text-destructive">
            {errors.password}
          </p>
        )}
      </div>

      <TurnstileField key={attempt} onToken={setCaptchaToken} />

      <Button type="submit" className="w-full" disabled={submitting}>
        {submitting ? m.auth_login_submitting() : m.auth_login_submit()}
      </Button>
    </form>
  )
}
