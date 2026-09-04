import { useEffect, useRef, useState } from 'react'
import { createFileRoute, Link, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import * as z from 'zod'
import { AuthCard } from '#/components/auth/AuthCard'
import { Button, buttonVariants } from '#/components/ui/button'
import { authErrorMessage } from '#/lib/auth-errors'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { requestEmailVerification, signOut, verifyEmail } from '#/api/auth'
import { useSession } from '#/lib/use-session'

export const Route = createFileRoute('/verify-email')({
  // `token` is what the verify_email mail links to (internal/auth.enqueueTokenMail builds
  // `${APP_URL}/verify-email?token=<t>`). `done`/`error` are kept for old links. TanStack's
  // default search parser turns `?done=1` into a number, so accept either without transforming.
  validateSearch: z.object({
    token: z.string().optional(),
    done: z.union([z.string(), z.number()]).optional(),
    error: z.string().optional(),
  }),
  component: VerifyEmailPage,
})

/**
 * Four states, decided in order:
 *   1. `?token=` present → consume it via POST /api/v1/auth/verify-email (public route), then the
 *      done card. The session, if any, is refreshed so the router sees `emailVerified: true`.
 *   2. Signed in and verified (or a legacy `?done=1`) → the done card.
 *   3. Signed in, unverified (the route guard sends every gated page here) → the pending card:
 *      resend (POST /email-verifications — needs exactly this session) and sign out.
 *   4. Signed out, no token → the expired card with a link to sign in.
 */
function VerifyEmailPage() {
  const { token, done } = Route.useSearch()
  const session = useSession()

  if (token) return <VerifyWithToken token={token} />
  if (String(done) === '1' || session?.user.emailVerified) return <VerifyDone />
  if (session) return <VerifyPending email={session.user.email} />
  return <VerifyExpired />
}

/** Exported for `verify-email.test.tsx`: the one piece of this route with real logic worth
 * unit-testing in isolation (the token round trip), rather than through the full route tree. */
export function VerifyWithToken({ token }: { token: string }) {
  const router = useRouter()
  const [state, setState] = useState<'verifying' | 'done' | 'error'>('verifying')
  const started = useRef(false)

  useEffect(() => {
    // The token is single-use; StrictMode's double effect (and a re-render) must not spend it
    // twice — the second attempt would report "expired" for a link that just worked.
    if (started.current) return
    started.current = true
    verifyEmail(token)
      .then(async () => {
        await router.invalidate()
        setState('done')
      })
      .catch(() => setState('error'))
  }, [router, token])

  if (state === 'verifying') {
    return (
      <AuthCard title={m.auth_verify_pending_title()}>
        <p className="text-sm text-muted-foreground" role="status">
          {m.auth_verify_verifying()}
        </p>
      </AuthCard>
    )
  }
  if (state === 'done') return <VerifyDone />
  return <VerifyExpired />
}

function VerifyDone() {
  return (
    <AuthCard title={m.auth_verify_done_title()}>
      <p className="text-sm text-muted-foreground">{m.auth_verify_done_body()}</p>
      <Link to="/dashboard" className={cn(buttonVariants(), 'w-full')}>
        {m.auth_verify_done_cta()}
      </Link>
    </AuthCard>
  )
}

function VerifyPending({ email }: { email: string }) {
  const router = useRouter()
  const navigate = useNavigate()
  const [submitting, setSubmitting] = useState(false)

  async function handleResend() {
    setSubmitting(true)
    try {
      await requestEmailVerification()
      toast.success(m.auth_verify_resent())
    } catch (error) {
      toast.error(authErrorMessage(error))
    } finally {
      setSubmitting(false)
    }
  }

  async function handleSignOut() {
    await signOut()
    await router.invalidate()
    await navigate({ to: '/' })
  }

  return (
    <AuthCard title={m.auth_verify_pending_title()} subtitle={m.auth_verify_pending_body({ email })}>
      <Button type="button" className="w-full" disabled={submitting} onClick={() => void handleResend()}>
        {submitting ? m.auth_verify_resend_submitting() : m.auth_verify_resend_submit()}
      </Button>
      <Button type="button" variant="ghost" className="w-full" onClick={() => void handleSignOut()}>
        {m.auth_verify_sign_out()}
      </Button>
    </AuthCard>
  )
}

function VerifyExpired() {
  return (
    <AuthCard title={m.auth_verify_error_title()} subtitle={m.auth_verify_error_body()}>
      <Link to="/login" className={cn(buttonVariants(), 'w-full')}>
        {m.auth_login_link()}
      </Link>
    </AuthCard>
  )
}
