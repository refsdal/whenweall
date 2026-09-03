import { createFileRoute, useNavigate, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import { AcceptInvitationCard } from '#/components/auth/AcceptInvitationCard'
import { m } from '#/lib/i18n'
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
 *
 * The lookup/switch is best-effort: the membership row is already committed by the time it runs
 * (`acceptInvitation` already resolved), so a failure here must not strand the user on this card —
 * a second click would re-submit an already-accepted invitation and surface the misleading
 * "invalid or expired" message. On failure we still navigate to /dashboard and toast that the
 * switch needs to be done by hand (the org switcher in `UserMenu` reaches it either way).
 */
export const Route = createFileRoute('/accept-invitation/$id')({
  beforeLoad: ({ context, params }) => requireVerifiedSession(context, `/accept-invitation/${params.id}`),
  component: AcceptInvitationPage,
})

function AcceptInvitationPage() {
  const { id } = Route.useParams()
  return <AcceptInvitationRoute invitationId={id} />
}

/**
 * Exported for `$id.test.tsx`: the post-accept org-switch logic, independent of
 * `Route.useParams()` (same split as `verify-email.tsx`'s `VerifyWithToken`).
 */
export function AcceptInvitationRoute({ invitationId }: { invitationId: string }) {
  const router = useRouter()
  const navigate = useNavigate()

  async function handleAccepted(orgSlug: string | null) {
    if (orgSlug) {
      try {
        const orgs = await listOrganizations()
        const joined = orgs.find((org) => org.slug === orgSlug)
        if (joined && !joined.active) await switchOrganization(joined.id)
      } catch {
        toast.error(m.org_invite_switch_failed())
      }
    }
    await router.invalidate()
    await navigate({ to: '/dashboard' })
  }

  return (
    <AcceptInvitationCard invitationId={invitationId} onAccepted={(orgSlug) => void handleAccepted(orgSlug)} />
  )
}
