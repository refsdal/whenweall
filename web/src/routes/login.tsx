import { useState, type FormEvent } from 'react'
import { createFileRoute, Link, redirect, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { GoogleButton } from '#/components/auth/GoogleButton'
import { PasskeySignInButton } from '#/components/auth/PasskeySignInButton'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Separator } from '#/components/ui/separator'
import { authErrorMessage } from '#/lib/auth-errors'
import { m } from '#/lib/i18n'
import { nextSearchSchema, safeNext } from '#/lib/search'
import { authClient } from '#/server/auth/client'

export const Route = createFileRoute('/login')({
  validateSearch: nextSearchSchema,
  beforeLoad: ({ context, search }) => {
    if (context.session) {
      throw redirect({ href: safeNext(search.next, '/dashboard') })
    }
  },
  component: LoginPage,
})

const emailSchema = z.email()

function LoginPage() {
  const { next } = Route.useSearch()
  const { publicConfig } = Route.useRouteContext()
  const router = useRouter()
  const navigate = useNavigate()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [errors, setErrors] = useState<{ email?: string; password?: string }>({})
  const [submitting, setSubmitting] = useState(false)
  const [unverifiedEmail, setUnverifiedEmail] = useState<string | null>(null)
  const [resending, setResending] = useState(false)
  const [resendCaptchaToken, setResendCaptchaToken] = useState<string | null>(null)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setUnverifiedEmail(null)

    const fieldErrors: typeof errors = {}
    if (!emailSchema.safeParse(email).success) fieldErrors.email = m.auth_error_email_invalid()
    if (password.length === 0) fieldErrors.password = m.auth_error_password_required()
    setErrors(fieldErrors)
    if (Object.keys(fieldErrors).length > 0) return
    if (!captchaToken) {
      toast.error(m.auth_error_captcha_required())
      return
    }

    setSubmitting(true)
    try {
      const { error } = await authClient.signIn.email({
        email,
        password,
        fetchOptions: { headers: { 'x-captcha-response': captchaToken } },
      })
      if (error) {
        if (error.status === 403) {
          setUnverifiedEmail(email)
          return
        }
        toast.error(authErrorMessage(error))
        return
      }
      await router.invalidate()
      await navigate({ href: safeNext(next) })
    } finally {
      setSubmitting(false)
    }
  }

  async function handleResend() {
    if (!unverifiedEmail || !resendCaptchaToken) return
    setResending(true)
    try {
      await authClient.sendVerificationEmail({
        email: unverifiedEmail,
        callbackURL: '/verify-email?done=1',
        fetchOptions: { headers: { 'x-captcha-response': resendCaptchaToken } },
      })
      toast.success(m.auth_verify_resent())
    } finally {
      setResending(false)
    }
  }

  return (
    <AuthCard
      title={m.auth_login_title()}
      subtitle={m.auth_login_subtitle()}
      footer={
        <>
          <span>
            {m.auth_login_no_account()}{' '}
            <Link
              to="/signup"
              search={{ next }}
              className="font-medium text-primary-ink hover:underline"
            >
              {m.auth_signup_link()}
            </Link>
          </span>
          <Link to="/forgot-password" className="text-primary-ink hover:underline">
            {m.auth_forgot_password_link()}
          </Link>
        </>
      }
    >
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

        {unverifiedEmail && (
          <div className="flex flex-col gap-2 rounded-lg border border-border bg-secondary/60 p-3 text-sm">
            <p>{m.auth_login_unverified()}</p>
            <TurnstileField onToken={setResendCaptchaToken} />
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={resending || !resendCaptchaToken}
              onClick={() => void handleResend()}
            >
              {m.auth_resend_verification()}
            </Button>
          </div>
        )}

        <TurnstileField onToken={setCaptchaToken} />

        <Button type="submit" className="w-full" disabled={submitting}>
          {submitting ? m.auth_login_submitting() : m.auth_login_submit()}
        </Button>
      </form>

      <div className="flex items-center gap-3">
        <Separator className="flex-1" />
        <span className="text-xs text-muted-foreground uppercase">{m.auth_or()}</span>
        <Separator className="flex-1" />
      </div>

      <div className="flex flex-col gap-2">
        <PasskeySignInButton next={safeNext(next)} />
        {publicConfig.googleEnabled && <GoogleButton next={safeNext(next)} />}
      </div>
    </AuthCard>
  )
}
