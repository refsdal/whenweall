import { useState, type FormEvent } from 'react'
import { createFileRoute, Link, redirect, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { GoogleButton } from '#/components/auth/GoogleButton'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Separator } from '#/components/ui/separator'
import { authErrorMessage } from '#/lib/auth-errors'
import { m } from '#/lib/i18n'
import { nextSearchSchema, safeNext } from '#/lib/search'
import { signInWithCredential } from '#/api/auth'

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

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

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
      // Limen's credential plugin has no unverified-email gate on signin at all (see the task
      // report) — the old "resend verification from this page" sub-flow (a 403 branch) had
      // nothing left to key off, so it's gone; verifying happens from `/verify-email` instead.
      await signInWithCredential(email, password)
      await router.invalidate()
      await navigate({ href: safeNext(next) })
    } catch (error) {
      toast.error(authErrorMessage(error))
    } finally {
      setSubmitting(false)
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

        <TurnstileField onToken={setCaptchaToken} />

        <Button type="submit" className="w-full" disabled={submitting}>
          {submitting ? m.auth_login_submitting() : m.auth_login_submit()}
        </Button>
      </form>

      {publicConfig.googleEnabled && (
        <>
          <div className="flex items-center gap-3">
            <Separator className="flex-1" />
            <span className="text-xs text-muted-foreground uppercase">{m.auth_or()}</span>
            <Separator className="flex-1" />
          </div>
          <GoogleButton next={safeNext(next)} />
        </>
      )}
    </AuthCard>
  )
}
