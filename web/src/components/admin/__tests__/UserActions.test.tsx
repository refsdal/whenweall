import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { UserActions } from '#/components/admin/UserActions'
import type { AdminUserDetail } from '#/api/types'

// Real `api()` client against msw — the same pattern as api/__tests__/client.test.ts — so what is
// under test is the whole wire contract (method, path, `{reason}` body), not a mocked module.
const server = setupServer()
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
})
afterAll(() => server.close())

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))
beforeEach(() => {
  toast.success.mockReset()
  toast.error.mockReset()
})

// Radix marks the body `pointer-events: none` while a dialog is open, which user-event refuses
// to click through by default (see ReasonDialog.test.tsx).
const user = () => userEvent.setup({ pointerEventsCheck: 0 })

function makeUser(overrides: Partial<AdminUserDetail> = {}): AdminUserDetail {
  return {
    id: '42',
    email: 'ada@example.com',
    name: 'Ada',
    emailVerified: true,
    staff: false,
    locked: false,
    createdAt: '2026-08-01T10:00:00.000Z',
    lockReason: null,
    orgs: [],
    counts: { polls: 0, bookingPages: 0, bookings: 0 },
    ...overrides,
  }
}

/** Opens the dialog behind `trigger`, types `reason`, and presses `confirm`. */
async function confirmWithReason(trigger: string, confirm: string, reason: string) {
  const u = user()
  await u.click(screen.getByRole('button', { name: trigger }))
  await u.type(screen.getByLabelText(/why/i), reason)
  // Dispatched directly rather than through user-event: the Radix overlay swallows the simulated
  // pointer sequence, and what is under test is the handler, not Radix.
  fireEvent.click(screen.getByRole('button', { name: confirm }))
}

describe('UserActions', () => {
  it('locks with the typed reason and asks the page to refetch', async () => {
    let seenBody: unknown = null
    server.use(
      http.post('/api/v1/admin/users/42/lock', async ({ request }) => {
        seenBody = await request.json()
        return HttpResponse.json({ ok: true })
      }),
    )
    const onChanged = vi.fn()
    render(<UserActions user={makeUser()} isSelf={false} onChanged={onChanged} onDeleted={vi.fn()} />)

    await confirmWithReason('Lock account', 'Lock', 'ticket 481')

    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce())
    expect(seenBody).toEqual({ reason: 'ticket 481' })
    expect(toast.success).toHaveBeenCalledWith('Account locked.')
  })

  it('offers unlock, not lock, for a locked account', async () => {
    let unlocked = false
    server.use(
      http.post('/api/v1/admin/users/42/unlock', () => {
        unlocked = true
        return HttpResponse.json({ ok: true })
      }),
    )
    const onChanged = vi.fn()
    render(
      <UserActions
        user={makeUser({ locked: true, lockReason: 'abuse report' })}
        isSelf={false}
        onChanged={onChanged}
        onDeleted={vi.fn()}
      />,
    )
    expect(screen.queryByRole('button', { name: 'Lock account' })).not.toBeInTheDocument()

    await confirmWithReason('Unlock account', 'Unlock', 'resolved')

    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce())
    expect(unlocked).toBe(true)
    expect(toast.success).toHaveBeenCalledWith('Account unlocked.')
  })

  it('deletes and hands off to onDeleted instead of refetching a row that no longer exists', async () => {
    let seenBody: unknown = null
    server.use(
      http.delete('/api/v1/admin/users/42', async ({ request }) => {
        seenBody = await request.json()
        return HttpResponse.json({ ok: true })
      }),
    )
    const onChanged = vi.fn()
    const onDeleted = vi.fn()
    render(<UserActions user={makeUser()} isSelf={false} onChanged={onChanged} onDeleted={onDeleted} />)

    await confirmWithReason('Delete account', 'Delete', 'gdpr request')

    await waitFor(() => expect(onDeleted).toHaveBeenCalledOnce())
    expect(onChanged).not.toHaveBeenCalled()
    expect(seenBody).toEqual({ reason: 'gdpr request' })
    expect(toast.success).toHaveBeenCalledWith('Account deleted.')
  })

  it('surfaces a backend rejection as an error toast and does not refetch', async () => {
    server.use(
      http.post('/api/v1/admin/users/42/lock', () =>
        HttpResponse.json(
          { error: { code: 'invalid', message: 'you cannot lock your own account' } },
          { status: 400 },
        ),
      ),
    )
    const onChanged = vi.fn()
    render(<UserActions user={makeUser()} isSelf={false} onChanged={onChanged} onDeleted={vi.fn()} />)

    await confirmWithReason('Lock account', 'Lock', 'oops')

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('you cannot lock your own account')),
    )
    expect(onChanged).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
  })

  // The backend 400s a staff member targeting themselves (internal/admin/handlers.go's
  // writeCannotTargetSelf); the UI must not make that 400 the way anyone finds out.
  it('shows no controls for the signed-in staff member themselves', () => {
    render(<UserActions user={makeUser()} isSelf onChanged={vi.fn()} onDeleted={vi.fn()} />)

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.getByText(/your own account/i)).toBeInTheDocument()
  })
})
