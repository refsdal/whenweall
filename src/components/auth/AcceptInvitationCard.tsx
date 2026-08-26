import { useState } from 'react'
import { AuthCard } from '#/components/auth/AuthCard'
import { Button } from '#/components/ui/button'
import { m } from '#/lib/i18n'
import { authClient } from '#/server/auth/client'

/**
 * Confirmation card for `/accept-invitation/$id`. Accepting is a real state change — it creates a
 * `member` row and switches the caller's active org — so it must ride on an explicit click, never
 * a mount effect: the route is the target of an emailed link reached by plain browser navigation,
 * and email "safe link" scanners (Defender, Proofpoint) and chat unfurlers hydrate the page with
 * zero human interaction. Kept as its own component (route wiring — `Route.useParams()`,
 * `useNavigate()` — stays in the route file) so it can be unit-tested without a router, same split
 * as `ManageBooking`/`booking/$id/index.tsx`.
 */
export function AcceptInvitationCard({
  invitationId,
  onAccepted,
}: {
  invitationId: string
  onAccepted: () => void
}) {
  const [submitting, setSubmitting] = useState(false)
  const [failed, setFailed] = useState(false)

  async function handleAccept() {
    setSubmitting(true)
    try {
      const { error } = await authClient.organization.acceptInvitation({ invitationId })
      if (error) {
        setFailed(true)
        return
      }
      onAccepted()
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthCard
      title={m.org_invite_accept_title()}
      subtitle={failed ? m.org_invite_invalid() : m.org_invite_accept_body()}
    >
      {!failed && (
        <Button className="w-full" disabled={submitting} onClick={() => void handleAccept()}>
          {m.org_invite_accept_cta()}
        </Button>
      )}
    </AuthCard>
  )
}
