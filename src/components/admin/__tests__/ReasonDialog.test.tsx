import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ReasonDialog } from '#/components/admin/ReasonDialog'

// This project has no `globals: true`, so React Testing Library cannot register its own
// auto-cleanup — every component test file unmounts explicitly. Without it the DOM accumulates
// across cases in a file and later queries resolve against an earlier render.
afterEach(() => cleanup())

// Radix marks the body `pointer-events: none` while a dialog is open, which user-event refuses
// to click through by default. The guard is about real pointer capture, not this.
const user = () => userEvent.setup({ pointerEventsCheck: 0 })

function setup(onConfirm = vi.fn()) {
  render(
    <ReasonDialog
      open
      onOpenChange={() => {}}
      title="Ban this user"
      confirmLabel="Ban"
      onConfirm={onConfirm}
    />,
  )
  return { onConfirm }
}

describe('ReasonDialog', () => {
  // Who, what and when are recorded whatever happens. *Why* only exists if someone types it, so
  // the dialog refuses to proceed without it.
  it('keeps the confirm button disabled until a reason is given', async () => {
    setup()
    const confirm = screen.getByRole('button', { name: 'Ban' })
    expect(confirm).toBeDisabled()

    await user().type(screen.getByLabelText(/why/i), 'ticket 481')

    expect(confirm).toBeEnabled()
  })

  it('treats whitespace as no reason at all', async () => {
    setup()
    await user().type(screen.getByLabelText(/why/i), '   ')

    expect(screen.getByRole('button', { name: 'Ban' })).toBeDisabled()
  })

  it('passes the reason to the action, trimmed', async () => {
    const { onConfirm } = setup()
    const u = user()
    await u.type(screen.getByLabelText(/why/i), 'ticket 481  ')

    // Dispatched directly rather than through user-event: the Radix overlay swallows the
    // simulated pointer sequence, and what is under test is the handler, not Radix.
    fireEvent.click(screen.getByRole('button', { name: 'Ban' }))

    await waitFor(() => expect(onConfirm).toHaveBeenCalledWith('ticket 481'))
  })
})
