import { useEffect, useState } from 'react'
import { createFileRoute, Link, redirect, useNavigate } from '@tanstack/react-router'
import { AuthCard } from '#/components/auth/AuthCard'
import { buttonVariants } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { authClient } from '#/server/auth/client'

/**
 * Where the OrgInvite email's CTA lands (see `sendInvitationEmail` in `#/server/auth/auth.ts`).
 * Signed out: bounce through `/login` with `next` pointing back here, same pattern as every other
 * authenticated route (`dashboard.tsx`, `settings.tsx`, ...). Signed in: fire
 * `organization.acceptInvitation` on mount and redirect to `/dashboard` on success — Better-Auth's
 * endpoint already switches the session's active org to the one just joined.
 */
export const Route = createFileRoute('/accept-invitation/$id')({
  beforeLoad: ({ context, params }) => {
    if (!context.session) {
      throw redirect({ to: '/login', search: { next: `/accept-invitation/${params.id}` } })
    }
  },
  component: AcceptInvitationPage,
})

function AcceptInvitationPage() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let cancelled = false
    void authClient.organization.acceptInvitation({ invitationId: id }).then(({ error }) => {
      if (cancelled) return
      if (error) {
        setFailed(true)
        return
      }
      void navigate({ to: '/dashboard' })
    })
    return () => {
      cancelled = true
    }
  }, [id, navigate])

  return (
    <AuthCard
      title={m.org_invite_accept_title()}
      subtitle={failed ? m.org_invite_invalid() : m.org_invite_accept_body()}
    >
      {failed && (
        <Link to="/dashboard" className={cn(buttonVariants(), 'w-full')}>
          {m.org_invite_accept_cta()}
        </Link>
      )}
    </AuthCard>
  )
}
