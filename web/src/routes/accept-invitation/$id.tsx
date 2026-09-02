import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { AcceptInvitationCard } from '#/components/auth/AcceptInvitationCard'

/**
 * Where the OrgInvite email's CTA lands (see `sendInvitationEmail` in `#/server/auth/auth.ts`).
 * Signed out: bounce through `/login` with `next` pointing back here, same pattern as every other
 * authenticated route (`dashboard.tsx`, `settings.tsx`, ...). Signed in: render a confirmation
 * card (`AcceptInvitationCard`) — acceptance only fires on the card's explicit button click, not
 * on route hydration, since this URL is also hit by email link-safety scanners and unfurlers.
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

  return (
    <AcceptInvitationCard
      invitationId={id}
      // Better-Auth's `acceptInvitation` endpoint already switches the session's active org to
      // the one just joined, so a plain redirect is enough.
      onAccepted={() => void navigate({ to: '/dashboard' })}
    />
  )
}
