import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CreatorWizard } from '#/components/creator/CreatorWizard'
import { m } from '#/lib/i18n'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))

const createPoll = vi.hoisted(() => vi.fn())
vi.mock('#/api/polls', async (importOriginal) => ({
  ...(await importOriginal<typeof import('#/api/polls')>()),
  createPoll,
}))

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

/** Drives the wizard from a blank draft up to (but not past) the final "Create poll" click: picks
 * the "options" type, gives it a title, adds one text option, and advances to the settings step. */
async function fillWizardToSubmit(user: ReturnType<typeof userEvent.setup>) {
  render(<CreatorWizard />)

  await user.click(screen.getByRole('button', { name: /anything else/i }))
  await user.type(screen.getByLabelText(/title/i), 'Team lunch')
  await user.click(screen.getByRole('button', { name: /^next$/i }))

  const firstOption = await screen.findByLabelText('Option 1')
  await user.type(firstOption, 'Pizza')
  await user.click(screen.getByRole('button', { name: /^next$/i }))

  return screen.findByRole('button', { name: /create poll/i })
}

describe('CreatorWizard submit failures', () => {
  it('shows an error toast and re-enables the submit button when the server fn rejects', async () => {
    const user = userEvent.setup()
    createPoll.mockRejectedValue(new Error('D1_ERROR: too many SQL variables at offset 440'))

    const submit = await fillWizardToSubmit(user)
    await user.click(submit)

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(m.creator_submit_error()))
    // The raw error internals never reach the user.
    expect(toast.error.mock.calls.flat().join(' ')).not.toContain('SQL variables')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /create poll/i })).toBeEnabled()
    })
  })

  it('does not show a success toast when the server fn rejects', async () => {
    const user = userEvent.setup()
    createPoll.mockRejectedValue(new Error('boom'))

    const submit = await fillWizardToSubmit(user)
    await user.click(submit)

    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(toast.success).not.toHaveBeenCalled()
  })
})
