import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { IdentitySheet } from '#/components/signup/IdentitySheet'

// The real widget loads Cloudflare's script and renders an iframe challenge — none of that runs in
// jsdom. Stand in with a button that fires the same callback the real component takes.
vi.mock('@marsidev/react-turnstile', () => ({
  Turnstile: ({ onSuccess }: { onSuccess: (token: string) => void }) => (
    <button type="button" onClick={() => onSuccess('tok')}>
      mock turnstile
    </button>
  ),
}))

afterEach(() => cleanup())

function renderSheet(overrides: Partial<React.ComponentProps<typeof IdentitySheet>> = {}) {
  const onSubmit = vi.fn()
  const onOpenChange = vi.fn()
  render(
    <IdentitySheet
      open
      onOpenChange={onOpenChange}
      requireEmail={false}
      needsCaptcha={false}
      defaultName=""
      submitting={false}
      onSubmit={onSubmit}
      {...overrides}
    />,
  )
  return { onSubmit, onOpenChange }
}

describe('IdentitySheet', () => {
  it('asks for a name before it submits', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderSheet()

    await user.click(screen.getByRole('button', { name: /sign me up/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/enter your name/i)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('requires an email when the poll does', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderSheet({ requireEmail: true })

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await user.click(screen.getByRole('button', { name: /sign me up/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/email/i)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('rejects an email that is not an email', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderSheet()

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await user.type(screen.getByLabelText(/email/i), 'not-an-email')
    await user.click(screen.getByRole('button', { name: /sign me up/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/valid email/i)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('submits the name and email it collected', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderSheet()

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
    await user.click(screen.getByRole('button', { name: /sign me up/i }))

    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Ada',
      email: 'ada@example.com',
      turnstileToken: undefined,
    })
  })

  it('waits for the captcha when a guest needs one, then passes its token', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderSheet({ needsCaptcha: true })

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await user.click(screen.getByRole('button', { name: /sign me up/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/verification/i)
    expect(onSubmit).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: /mock turnstile/i }))
    await user.click(screen.getByRole('button', { name: /sign me up/i }))

    expect(onSubmit).toHaveBeenCalledWith({
      name: 'Ada',
      email: undefined,
      turnstileToken: 'tok',
    })
  })

  it('renders nothing while closed', () => {
    renderSheet({ open: false })

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
