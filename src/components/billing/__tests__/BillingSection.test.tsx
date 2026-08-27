import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BillingSection } from '#/components/billing/BillingSection'
import { authClient } from '#/server/auth/client'
import { m } from '#/lib/i18n'

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

vi.mock('#/server/auth/client', () => ({
  authClient: {
    subscription: {
      upgrade: vi.fn(),
      billingPortal: vi.fn(),
    },
  },
}))

const FREE = {
  plan: 'free' as const,
  maxSeats: 1 as const,
  googleSync: false,
  branding: false,
  push: false,
}
const PREMIUM = {
  plan: 'premium' as const,
  maxSeats: 10 as const,
  googleSync: true,
  branding: true,
  push: true,
}

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('BillingSection', () => {
  it('renders nothing for a non-owner', () => {
    const { container } = render(
      <BillingSection
        orgId="org_1"
        role="admin"
        entitlements={FREE}
        subscription={null}
        seatsUsed={1}
      />,
    )

    expect(container).toBeEmptyDOMElement()
  })

  it('shows an upgrade CTA for a free-plan owner', () => {
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={FREE}
        subscription={null}
        seatsUsed={1}
      />,
    )

    expect(screen.getByRole('button', { name: /upgrade to premium/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /manage billing/i })).not.toBeInTheDocument()
  })

  it('shows a subtitle under the section heading, like every sibling settings section', () => {
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={FREE}
        subscription={null}
        seatsUsed={1}
      />,
    )

    expect(screen.getByText(m.billing_subtitle())).toBeInTheDocument()
  })

  it('gives the plan-status card the same surface background as every other bordered card', () => {
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={FREE}
        subscription={null}
        seatsUsed={1}
      />,
    )

    expect(screen.getByText(m.billing_free_plan()).closest('div')).toHaveClass('bg-card')
  })

  it('calls subscription.upgrade with the org as referenceId and disables the button while pending', async () => {
    let resolveUpgrade!: (value: { data: null; error: null }) => void
    vi.mocked(authClient.subscription.upgrade).mockReturnValue(
      new Promise((resolve) => {
        resolveUpgrade = resolve
      }) as never,
    )
    const user = userEvent.setup()
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={FREE}
        subscription={null}
        seatsUsed={1}
      />,
    )

    const button = screen.getByRole('button', { name: /upgrade to premium/i })
    await user.click(button)

    expect(button).toBeDisabled()
    expect(authClient.subscription.upgrade).toHaveBeenCalledExactlyOnceWith({
      plan: 'premium',
      referenceId: 'org_1',
      annual: false,
      successUrl: '/settings?upgraded=1',
      cancelUrl: '/settings',
    })

    resolveUpgrade({ data: null, error: null })
    await vi.waitFor(() => {
      expect(screen.getByRole('button', { name: /upgrade to premium/i })).toBeEnabled()
    })
  })

  it('passes annual: true once the annual toggle is selected', async () => {
    vi.mocked(authClient.subscription.upgrade).mockResolvedValue({
      data: null,
      error: null,
    } as never)
    const user = userEvent.setup()
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={FREE}
        subscription={null}
        seatsUsed={1}
      />,
    )

    await user.click(screen.getByRole('button', { name: /annual/i }))
    await user.click(screen.getByRole('button', { name: /upgrade to premium/i }))

    expect(authClient.subscription.upgrade).toHaveBeenCalledExactlyOnceWith(
      expect.objectContaining({ annual: true }),
    )
  })

  it('shows a toast when upgrade fails', async () => {
    vi.mocked(authClient.subscription.upgrade).mockResolvedValue({
      data: null,
      error: { message: 'nope' },
    } as never)
    const user = userEvent.setup()
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={FREE}
        subscription={null}
        seatsUsed={1}
      />,
    )

    await user.click(screen.getByRole('button', { name: /upgrade to premium/i }))

    expect(await screen.findByRole('button', { name: /upgrade to premium/i })).toBeEnabled()
    expect(toast.error).toHaveBeenCalled()
  })

  it('shows plan status, seat usage and a manage-billing button for a premium owner', () => {
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={PREMIUM}
        subscription={{
          status: 'active',
          periodEnd: Date.parse('2026-09-24'),
          cancelAtPeriodEnd: false,
        }}
        seatsUsed={3}
      />,
    )

    expect(screen.getByText(/3 of 10 seats used/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /manage billing/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /upgrade to premium/i })).not.toBeInTheDocument()
    expect(screen.getByText(/renews/i)).toBeInTheDocument()
  })

  it('shows a cancellation note when cancelAtPeriodEnd is true', () => {
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={PREMIUM}
        subscription={{
          status: 'active',
          periodEnd: Date.parse('2026-09-24'),
          cancelAtPeriodEnd: true,
        }}
        seatsUsed={3}
      />,
    )

    expect(screen.getByText(/cancels/i)).toBeInTheDocument()
    expect(screen.queryByText(/^renews/i)).not.toBeInTheDocument()
  })

  it('calls subscription.billingPortal with the org as referenceId and disables the button while pending', async () => {
    let resolvePortal!: (value: { data: null; error: null }) => void
    vi.mocked(authClient.subscription.billingPortal).mockReturnValue(
      new Promise((resolve) => {
        resolvePortal = resolve
      }) as never,
    )
    const user = userEvent.setup()
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={PREMIUM}
        subscription={{ status: 'active', periodEnd: null, cancelAtPeriodEnd: false }}
        seatsUsed={2}
      />,
    )

    const button = screen.getByRole('button', { name: /manage billing/i })
    await user.click(button)

    expect(button).toBeDisabled()
    expect(authClient.subscription.billingPortal).toHaveBeenCalledExactlyOnceWith({
      referenceId: 'org_1',
      returnUrl: '/settings',
    })

    resolvePortal({ data: null, error: null })
    await vi.waitFor(() => {
      expect(screen.getByRole('button', { name: /manage billing/i })).toBeEnabled()
    })
  })

  it('shows a toast when the billing portal redirect fails', async () => {
    vi.mocked(authClient.subscription.billingPortal).mockResolvedValue({
      data: null,
      error: { message: 'nope' },
    } as never)
    const user = userEvent.setup()
    render(
      <BillingSection
        orgId="org_1"
        role="owner"
        entitlements={PREMIUM}
        subscription={{ status: 'active', periodEnd: null, cancelAtPeriodEnd: false }}
        seatsUsed={2}
      />,
    )

    await user.click(screen.getByRole('button', { name: /manage billing/i }))

    expect(await screen.findByRole('button', { name: /manage billing/i })).toBeEnabled()
    expect(toast.error).toHaveBeenCalled()
  })
})
