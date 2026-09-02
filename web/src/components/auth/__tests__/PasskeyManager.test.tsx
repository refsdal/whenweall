import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PasskeyManager } from '#/components/auth/PasskeyManager'
import { authClient } from '#/server/auth/client'

vi.mock('#/server/auth/client', () => ({
  authClient: {
    passkey: {
      listUserPasskeys: vi.fn(),
      addPasskey: vi.fn(),
      deletePasskey: vi.fn(),
    },
  },
}))

const passkeys = [
  { id: 'pk_1', name: 'MacBook Touch ID', createdAt: new Date('2026-01-01') },
  { id: 'pk_2', name: 'YubiKey', createdAt: new Date('2026-02-01') },
]

beforeEach(() => {
  vi.mocked(authClient.passkey.listUserPasskeys).mockResolvedValue({
    data: passkeys,
    error: null,
  } as never)
  vi.mocked(authClient.passkey.deletePasskey).mockResolvedValue({
    data: { status: true },
    error: null,
  } as never)
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('PasskeyManager', () => {
  it('lists the signed-in user passkeys', async () => {
    render(<PasskeyManager />)

    expect(await screen.findByText('MacBook Touch ID')).toBeInTheDocument()
    expect(screen.getByText('YubiKey')).toBeInTheDocument()
  })

  it('deletes a passkey after the delete is confirmed', async () => {
    const user = userEvent.setup()
    render(<PasskeyManager />)
    await screen.findByText('MacBook Touch ID')

    await user.click(screen.getByRole('button', { name: /delete macbook touch id/i }))
    await user.click(screen.getByRole('button', { name: /confirm/i }))

    await waitFor(() => {
      expect(authClient.passkey.deletePasskey).toHaveBeenCalledWith({ id: 'pk_1' })
    })
  })
})
