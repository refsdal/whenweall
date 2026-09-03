import { createFileRoute, useNavigate, useRouter } from '@tanstack/react-router'
import { AcceptInvitationCard } from '#/components/auth/AcceptInvitationCard'
import { requireVerifiedSession } from '#/lib/session-guard'
import { listOrganizations, switchOrganization } from '#/api/auth'

/**
 * Where the org_invite mail's CTA lands (internal/auth.enqueueInviteMail builds
 * `${APP_URL}/accept-invitation/<token>`). Signed out or unverified: bounce through /login or
 * /verify-email with `next` pointing back here, same as every other authenticated route. Signed
 * in: render a confirmation card (`AcceptInvitationCard`) — acceptance only fires on the card's
 * explicit button click, not on route hydration, since this URL is also hit by email link-safety
 * scanners and unfurlers.
 *
 * Limen's respond-to-invitation route inserts the member row but does NOT change the session's
 * active organization (unlike Better-Auth's acceptInvitation, which the old comment here relied
 * on), so after accepting we look the joined org up by slug and switch to it ourselves — otherwise
 * the user would land on their personal org's dashboard with the new org unreachable.
 */
export const Route = createFileRoute('/accept-invitation/$id')({
  beforeLoad: ({ context, params }) => requireVerifiedSession(context, `/accept-invitation/${params.id}`),
  component: AcceptInvitationPage,
})

function AcceptInvitationPage() {
  const { id } = Route.useParams()
  const router = useRouter()
  const navigate = useNavigate()

  async function handleAccepted(orgSlug: string | null) {
    if (orgSlug) {
      const orgs = await listOrganizations()
      const joined = orgs.find((org) => org.slug === orgSlug)
      if (joined && !joined.active) await switchOrganization(joined.id)
    }
    await router.invalidate()
    await navigate({ to: '/dashboard' })
  }

  return <AcceptInvitationCard invitationId={id} onAccepted={(orgSlug) => void handleAccepted(orgSlug)} />
}
