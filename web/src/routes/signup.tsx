import { useState, type FormEvent } from 'react'
import { createFileRoute, Link, redirect } from '@tanstack/react-router'
import { toast } from 'sonner'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { GoogleButton } from '#/components/auth/GoogleButton'
import { TurnstileField } from '#/components/auth/TurnstileField'
import { Button, buttonVariants } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Separator } from '#/components/ui/separator'
import { authErrorMessage } from '#/lib/auth-errors'
import { useCaptchaEnabled } from '#/lib/captcha'
import { m } from '#/lib/i18n'
import { nextSearchSchema, safeNext } from '#/lib/search'
import { cn } from '#/lib/utils'
import { signUpWithCredential } from '#/api/auth'

export const Route = createFileRoute('/signup')({
  validateSearch: nextSearchSchema,
  beforeLoad: ({ context, search }) => {
    if (context.session) {
      throw redirect({ href: safeNext(search.next, '/dashboard') })
    }
  },
  component: SignupPage,
})

const emailSchema = z.email()

type PasswordStrength = 0 | 1 | 2 | 3

function passwordStrength(password: string): PasswordStrength {
  if (password.length < 12) return 0
  let score: PasswordStrength = 1
  if (/[0-9]/.test(password) && /[a-zA-Z]/.test(password)) score = 2
  if (score === 2 && (password.length >= 16 || /[^a-zA-Z0-9]/.test(password))) score = 3
  return score
}

const STRENGTH_LABEL = [
  null,
  m.auth_password_strength_weak,
  m.auth_password_strength_fair,
  m.auth_password_strength_strong,
] as const

const STRENGTH_CLASS = ['bg-border', 'bg-destructive', 'bg-ifneedbe', 'bg-yes'] as const

function SignupPage() {
  const { next } = Route.useSearch()
  const { publicConfig } = Route.useRouteContext()
  const captchaEnabled = useCaptchaEnabled()

  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [captchaToken, setCaptchaToken] = useState<string | null>(null)
  const [errors, setErrors] = useState<{ name?: string; email?: string; password?: string }>({})
  const [submitting, setSubmitting] = useState(false)
  const [submittedEmail, setSubmittedEmail] = useState<string | null>(null)

  const strength = passwordStrength(password)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    const fieldErrors: typeof errors = {}
    if (!name.trim()) fieldErrors.name = m.auth_error_name_required()
    else if (name.trim().length > 80) fieldErrors.name = m.auth_error_name_too_long()
    if (!emailSchema.safeParse(email).success) fieldErrors.email = m.auth_error_email_invalid()
    if (password.length < 12) fieldErrors.password = m.auth_error_password_too_short()
    setErrors(fieldErrors)
    if (Object.keys(fieldErrors).length > 0) return
    if (captchaEnabled && !captchaToken) {
      toast.error(m.auth_error_captcha_required())
      return
    }

    setSubmitting(true)
    try {
      await signUpWithCredential(email, password, name.trim())
      setSubmittedEmail(email)
    } catch (error) {
      toast.error(authErrorMessage(error))
    } finally {
      setSubmitting(false)
    }
  }

  if (submittedEmail) {
    return (
      <AuthCard title={m.auth_signup_success_title()}>
        <p className="text-sm text-muted-foreground">
          {m.auth_signup_success_body({ email: submittedEmail })}
        </p>
        <Link to="/login" search={{ next }} className={cn(buttonVariants(), 'w-full')}>
          {m.auth_signup_success_back()}
        </Link>
      </AuthCard>
    )
  }

  return (
    <AuthCard
      title={m.auth_signup_title()}
      subtitle={m.auth_signup_subtitle()}
      footer={
        <span>
          {m.auth_signup_has_account()}{' '}
          <Link
            to="/login"
            search={{ next }}
            className="font-medium text-primary-ink hover:underline"
          >
            {m.auth_login_link()}
          </Link>
        </span>
      }
    >
      <form onSubmit={(e) => void handleSubmit(e)} noValidate className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="signup-name">{m.auth_name_label()}</Label>
          <Input
            id="signup-name"
            autoComplete="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            aria-invalid={!!errors.name}
            aria-describedby={errors.name ? 'signup-name-error' : undefined}
          />
          {errors.name && (
            <p id="signup-name-error" className="text-sm text-destructive">
              {errors.name}
            </p>
          )}
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="signup-email">{m.auth_email_label()}</Label>
          <Input
            id="signup-email"
            type="email"
            autoComplete="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            aria-invalid={!!errors.email}
            aria-describedby={errors.email ? 'signup-email-error' : undefined}
          />
          {errors.email && (
            <p id="signup-email-error" className="text-sm text-destructive">
              {errors.email}
            </p>
          )}
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="signup-password">{m.auth_password_label()}</Label>
          <Input
            id="signup-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={!!errors.password}
            aria-describedby="signup-password-hint signup-password-error"
          />
          <div className="flex items-center gap-1.5" aria-hidden="true">
            {[1, 2, 3].map((step) => (
              <span
                key={step}
                className={`h-1 flex-1 rounded-full ${step <= strength ? STRENGTH_CLASS[strength] : 'bg-border'}`}
              />
            ))}
          </div>
          <p id="signup-password-hint" className="text-sm text-muted-foreground">
            {password.length > 0 && strength > 0
              ? STRENGTH_LABEL[strength]?.()
              : m.auth_password_hint()}
          </p>
          {errors.password && (
            <p id="signup-password-error" className="text-sm text-destructive">
              {errors.password}
            </p>
          )}
        </div>

        <TurnstileField onToken={setCaptchaToken} />

        <Button type="submit" className="w-full" disabled={submitting}>
          {submitting ? m.auth_signup_submitting() : m.auth_signup_submit()}
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
