import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AcceptInvitationCard } from '#/components/auth/AcceptInvitationCard'
import { acceptInvitation } from '#/api/auth'

vi.mock('#/api/auth', () => ({
  acceptInvitation: vi.fn(),
}))

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('AcceptInvitationCard', () => {
  it('does not call acceptInvitation on render — this route is hit by email link scanners with no human present', () => {
    render(<AcceptInvitationCard invitationId="inv_1" onAccepted={vi.fn()} />)

    expect(acceptInvitation).not.toHaveBeenCalled()
  })

  it('calls acceptInvitation and onAccepted when the accept button is clicked', async () => {
    vi.mocked(acceptInvitation).mockResolvedValue(undefined)
    const onAccepted = vi.fn()
    const user = userEvent.setup()
    render(<AcceptInvitationCard invitationId="inv_1" onAccepted={onAccepted} />)

    await user.click(screen.getByRole('button', { name: /accept invitation/i }))

    expect(acceptInvitation).toHaveBeenCalledExactlyOnceWith('inv_1')
    expect(onAccepted).toHaveBeenCalledOnce()
  })

  it('shows the invalid/expired message and never calls onAccepted when acceptance fails', async () => {
    vi.mocked(acceptInvitation).mockRejectedValue(new Error('invitation not found'))
    const onAccepted = vi.fn()
    const user = userEvent.setup()
    render(<AcceptInvitationCard invitationId="inv_1" onAccepted={onAccepted} />)

    await user.click(screen.getByRole('button', { name: /accept invitation/i }))

    expect(await screen.findByText(/invalid or has expired/i)).toBeInTheDocument()
    expect(onAccepted).not.toHaveBeenCalled()
    // The accept button disappears once the invitation is known to be dead — nothing left to retry.
    expect(screen.queryByRole('button', { name: /accept invitation/i })).not.toBeInTheDocument()
  })
})
