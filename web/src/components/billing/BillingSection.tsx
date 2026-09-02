import { useState } from 'react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import { getLocale, intlLocale, m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { authClient } from '#/server/auth/client'
import type { OrgRole } from '#/server/auth/org-roles'
import type { Entitlements } from '#/server/billing/entitlements'
import { PREMIUM_PLAN_NAME } from '#/lib/billing'

export type BillingSubscriptionSnapshot = {
  status: string
  periodEnd: number | null
  cancelAtPeriodEnd: boolean
}

export type BillingSectionProps = {
  /** The org billing actions are scoped to — passed as `referenceId` to every subscription call. */
  orgId: string
  role: OrgRole
  entitlements: Entitlements
  subscription: BillingSubscriptionSnapshot | null
  seatsUsed: number
}

function formatDate(ms: number): string {
  return new Intl.DateTimeFormat(intlLocale(getLocale()), { dateStyle: 'medium' }).format(
    new Date(ms),
  )
}

/**
 * Settings' owner-only Billing section (spec §4). Free orgs get an upgrade CTA with a
 * monthly/annual toggle; Premium orgs get plan status, seat usage and a "Manage billing" link
 * into the Stripe portal. Returns `null` outright for anyone but the org owner — defence in
 * depth alongside the caller's own gate (`settings.tsx` mirrors the pattern used for the
 * booking-handle section).
 */
export function BillingSection({
  orgId,
  role,
  entitlements,
  subscription,
  seatsUsed,
}: BillingSectionProps) {
  const [annual, setAnnual] = useState(false)
  const [upgrading, setUpgrading] = useState(false)
  const [managing, setManaging] = useState(false)

  if (role !== 'owner') return null

  async function handleUpgrade() {
    setUpgrading(true)
    try {
      const { error } = await authClient.subscription.upgrade({
        plan: PREMIUM_PLAN_NAME,
        referenceId: orgId,
        annual,
        successUrl: '/settings?upgraded=1',
        cancelUrl: '/settings',
      })
      if (error) toast.error(m.auth_error_generic())
    } finally {
      setUpgrading(false)
    }
  }

  async function handleManage() {
    setManaging(true)
    try {
      const { error } = await authClient.subscription.billingPortal({
        referenceId: orgId,
        returnUrl: '/settings',
      })
      if (error) toast.error(m.auth_error_generic())
    } finally {
      setManaging(false)
    }
  }

  return (
    <section className="flex flex-col gap-3">
      <div>
        <h2 className="text-sm font-semibold">{m.billing_title()}</h2>
        <p className="text-sm text-muted-foreground">{m.billing_subtitle()}</p>
      </div>

      {entitlements.plan === 'free' ? (
        <div className="flex flex-col items-start gap-3 rounded-lg border border-border bg-card p-4">
          <p className="text-sm font-medium">{m.billing_free_plan()}</p>
          <div className="inline-flex rounded-full border border-border p-0.5 text-xs">
            <button
              type="button"
              aria-pressed={!annual}
              onClick={() => setAnnual(false)}
              className={cn(
                'rounded-full px-3 py-1 transition-colors',
                !annual && 'bg-secondary font-medium',
              )}
            >
              {m.billing_monthly()}
            </button>
            <button
              type="button"
              aria-pressed={annual}
              onClick={() => setAnnual(true)}
              className={cn(
                'rounded-full px-3 py-1 transition-colors',
                annual && 'bg-secondary font-medium',
              )}
            >
              {m.billing_annual()}
            </button>
          </div>
          <Button type="button" disabled={upgrading} onClick={() => void handleUpgrade()}>
            {m.billing_upgrade_cta()}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col items-start gap-2 rounded-lg border border-border bg-card p-4">
          <p className="text-sm font-medium">{m.billing_premium_plan()}</p>
          <p className="text-sm text-muted-foreground">
            {m.billing_seats_used({ used: seatsUsed, max: entitlements.maxSeats })}
          </p>
          {subscription?.periodEnd != null && (
            <p className="text-sm text-muted-foreground">
              {subscription.cancelAtPeriodEnd
                ? m.billing_cancels({ date: formatDate(subscription.periodEnd) })
                : m.billing_renews({ date: formatDate(subscription.periodEnd) })}
            </p>
          )}
          <Button
            type="button"
            variant="outline"
            disabled={managing}
            onClick={() => void handleManage()}
          >
            {m.billing_manage_cta()}
          </Button>
        </div>
      )}
    </section>
  )
}
