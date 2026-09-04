import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AcceptInvitationRoute } from '#/routes/accept-invitation/$id'
import { acceptInvitation, listOrganizations, switchOrganization } from '#/api/auth'

const invalidateMock = vi.fn().mockResolvedValue(undefined)
const navigateMock = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  createFileRoute: () => (options: unknown) => options,
  useRouter: () => ({ invalidate: invalidateMock }),
  useNavigate: () => navigateMock,
}))

vi.mock('#/api/auth', () => ({
  acceptInvitation: vi.fn(),
  listOrganizations: vi.fn(),
  switchOrganization: vi.fn(),
}))

const toastError = vi.fn()
vi.mock('sonner', () => ({
  toast: { error: (...args: unknown[]) => toastError(...args), success: vi.fn() },
}))

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('AcceptInvitationRoute', () => {
  it('switches to the joined org and lands on /dashboard when the lookup/switch succeeds', async () => {
    vi.mocked(acceptInvitation).mockResolvedValue({ orgSlug: 'team-ada' })
    vi.mocked(listOrganizations).mockResolvedValue([
      { id: 'org_1', name: 'Personal', slug: 'personal', active: true },
      { id: 'org_2', name: 'Team Ada', slug: 'team-ada', active: false },
    ])
    vi.mocked(switchOrganization).mockResolvedValue(undefined)
    const user = userEvent.setup()

    render(<AcceptInvitationRoute invitationId="inv_1" />)
    await user.click(screen.getByRole('button', { name: /accept invitation/i }))

    await waitFor(() => expect(navigateMock).toHaveBeenCalledExactlyOnceWith({ to: '/dashboard' }))
    expect(switchOrganization).toHaveBeenCalledExactlyOnceWith('org_2')
    expect(invalidateMock).toHaveBeenCalledOnce()
    expect(toastError).not.toHaveBeenCalled()
  })

  it('still lands on /dashboard and warns when the post-accept org lookup/switch fails', async () => {
    // The membership row is already created at this point (acceptInvitation resolved) — only the
    // best-effort auto-switch that follows is what fails here.
    vi.mocked(acceptInvitation).mockResolvedValue({ orgSlug: 'team-ada' })
    vi.mocked(listOrganizations).mockRejectedValue(new Error('network error'))
    const user = userEvent.setup()

    render(<AcceptInvitationRoute invitationId="inv_1" />)
    await user.click(screen.getByRole('button', { name: /accept invitation/i }))

    await waitFor(() => expect(navigateMock).toHaveBeenCalledExactlyOnceWith({ to: '/dashboard' }))
    expect(invalidateMock).toHaveBeenCalledOnce()
    expect(toastError).toHaveBeenCalledOnce()
    expect(switchOrganization).not.toHaveBeenCalled()
  })
})
