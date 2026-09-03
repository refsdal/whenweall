import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DeleteAccountDialog } from '#/components/auth/DeleteAccountDialog'

afterEach(() => cleanup())

describe('DeleteAccountDialog', () => {
  it('asks a credential account for its password and passes it to onDelete', async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(<DeleteAccountDialog hasPassword onDelete={onDelete} />)

    await user.click(screen.getByRole('button', { name: /^delete account$/i }))
    // Trigger and confirm share the label ("Delete account"); once the dialog is open the confirm
    // is the last matching button (Radix hides the page behind the modal from the a11y tree).
    const confirm = screen.getAllByRole('button', { name: /^delete account$/i }).at(-1)!
    expect(confirm).toBeDisabled()

    await user.type(screen.getByLabelText(/^password$/i), 'hunter2hunter2')
    expect(confirm).toBeEnabled()
    await user.click(confirm)

    expect(onDelete).toHaveBeenCalledExactlyOnceWith('hunter2hunter2')
  })

  it('needs no password for an OAuth-only account', async () => {
    const onDelete = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(<DeleteAccountDialog hasPassword={false} onDelete={onDelete} />)

    await user.click(screen.getByRole('button', { name: /^delete account$/i }))
    expect(screen.queryByLabelText(/^password$/i)).not.toBeInTheDocument()
    await user.click(screen.getAllByRole('button', { name: /^delete account$/i }).at(-1)!)

    expect(onDelete).toHaveBeenCalledExactlyOnceWith(undefined)
  })
})
