import { createFileRoute, Link, redirect, useNavigate, useRouter } from '@tanstack/react-router'
import { AuthCard } from '#/components/auth/AuthCard'
import { CredentialLoginForm } from '#/components/auth/CredentialLoginForm'
import { GoogleButton } from '#/components/auth/GoogleButton'
import { OidcButton } from '#/components/auth/OidcButton'
import { Separator } from '#/components/ui/separator'
import { m } from '#/lib/i18n'
import { nextSearchSchema, safeNext } from '#/lib/search'

export const Route = createFileRoute('/login')({
  validateSearch: nextSearchSchema,
  beforeLoad: ({ context, search }) => {
    if (context.session) {
      // An unverified session has nowhere useful to go but the verify page.
      if (!context.session.user.emailVerified) throw redirect({ to: '/verify-email', search: {} })
      throw redirect({ href: safeNext(search.next, '/dashboard') })
    }
  },
  component: LoginPage,
})

function LoginPage() {
  const { next } = Route.useSearch()
  const { publicConfig } = Route.useRouteContext()
  const router = useRouter()
  const navigate = useNavigate()

  const showProviders = publicConfig.googleEnabled || publicConfig.oidcEnabled

  return (
    <AuthCard
      title={m.auth_login_title()}
      subtitle={m.auth_login_subtitle()}
      footer={
        <>
          <span>
            {m.auth_login_no_account()}{' '}
            <Link to="/signup" search={{ next }} className="font-medium text-primary-ink hover:underline">
              {m.auth_signup_link()}
            </Link>
          </span>
          <Link to="/forgot-password" className="text-primary-ink hover:underline">
            {m.auth_forgot_password_link()}
          </Link>
        </>
      }
    >
      <CredentialLoginForm
        onSignedIn={async () => {
          await router.invalidate()
          await navigate({ href: safeNext(next) })
        }}
      />

      {showProviders && (
        <>
          <div className="flex items-center gap-3">
            <Separator className="flex-1" />
            <span className="text-xs text-muted-foreground uppercase">{m.auth_or()}</span>
            <Separator className="flex-1" />
          </div>
          {publicConfig.googleEnabled && <GoogleButton next={safeNext(next)} />}
          {publicConfig.oidcEnabled && publicConfig.oidcName && (
            <OidcButton provider={publicConfig.oidcName} name={publicConfig.oidcName} next={safeNext(next)} />
          )}
        </>
      )}
    </AuthCard>
  )
}
