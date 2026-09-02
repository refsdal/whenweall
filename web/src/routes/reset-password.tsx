import { useState, type FormEvent } from 'react'
import { createFileRoute, Link } from '@tanstack/react-router'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { Button, buttonVariants } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { authErrorMessage } from '#/lib/auth-errors'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { authClient } from '#/server/auth/client'

export const Route = createFileRoute('/reset-password')({
  validateSearch: z.object({ token: z.string().optional() }),
  component: ResetPasswordPage,
})

function ResetPasswordPage() {
  const { token } = Route.useSearch()

  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [done, setDone] = useState(false)

  if (!token) {
    return (
      <AuthCard title={m.auth_reset_missing_token_title()}>
        <p className="text-sm text-muted-foreground">{m.auth_reset_missing_token_body()}</p>
        <Link to="/forgot-password" className={cn(buttonVariants(), 'w-full')}>
          {m.auth_reset_missing_token_cta()}
        </Link>
      </AuthCard>
    )
  }

  if (done) {
    return (
      <AuthCard title={m.auth_reset_success_title()}>
        <p className="text-sm text-muted-foreground">{m.auth_reset_success_body()}</p>
        <Link to="/login" className={cn(buttonVariants(), 'w-full')}>
          {m.auth_reset_success_cta()}
        </Link>
      </AuthCard>
    )
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()

    if (password.length < 12) {
      setError(m.auth_error_password_too_short())
      return
    }
    setError(null)

    setSubmitting(true)
    try {
      const { error: resetError } = await authClient.resetPassword({
        newPassword: password,
        token,
      })
      if (resetError) {
        setError(authErrorMessage(resetError))
        return
      }
      setDone(true)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthCard title={m.auth_reset_title()} subtitle={m.auth_reset_subtitle()}>
      <form onSubmit={(e) => void handleSubmit(e)} noValidate className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="reset-password">{m.auth_new_password_label()}</Label>
          <Input
            id="reset-password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            aria-invalid={!!error}
            aria-describedby={error ? 'reset-password-error' : undefined}
          />
          {error && (
            <p id="reset-password-error" className="text-sm text-destructive">
              {error}
            </p>
          )}
        </div>

        <Button type="submit" className="w-full" disabled={submitting}>
          {submitting ? m.auth_reset_submitting() : m.auth_reset_submit()}
        </Button>
      </form>
    </AuthCard>
  )
}
