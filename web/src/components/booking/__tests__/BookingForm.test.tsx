import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BookingForm } from '#/components/booking/BookingForm'
import { useCaptchaEnabled, useTurnstileSiteKey } from '#/lib/captcha'

// The real widget loads Cloudflare's script and renders an iframe challenge — none of that runs
// in jsdom. Same stand-in as `TurnstileField.test.tsx`: a button that fires `onSuccess`.
vi.mock('@marsidev/react-turnstile', () => ({
  Turnstile: ({ onSuccess }: { onSuccess: (token: string) => void }) => (
    <button type="button" onClick={() => onSuccess('tok')}>
      mock turnstile
    </button>
  ),
}))

vi.mock('#/lib/captcha', () => ({
  useTurnstileSiteKey: vi.fn(() => 'site-key'),
  useCaptchaEnabled: vi.fn(() => true),
}))

afterEach(() => cleanup())

const SLOT = { start: '2026-09-15T07:00:00.000Z', end: '2026-09-15T07:30:00.000Z' }

function renderForm(overrides: Partial<React.ComponentProps<typeof BookingForm>> = {}) {
  const onSubmit = vi.fn()
  render(
    <BookingForm
      open
      onOpenChange={vi.fn()}
      title="Intro call"
      slot={SLOT}
      timeZone="Europe/Oslo"
      onSubmit={onSubmit}
      {...overrides}
    />,
  )
  return { onSubmit }
}

async function solveCaptcha(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'mock turnstile' }))
}

describe('BookingForm', () => {
  it('summarises the chosen slot in the viewer timezone', () => {
    renderForm()

    expect(screen.getByTestId('booking-form-summary')).toHaveTextContent('09:00')
    expect(screen.getByTestId('booking-form-summary')).toHaveTextContent('09:30')
  })

  it('requires a name', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
    await solveCaptcha(user)
    await user.click(screen.getByRole('button', { name: /confirm booking/i }))

    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })

  it('requires an email', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await solveCaptcha(user)
    await user.click(screen.getByRole('button', { name: /confirm booking/i }))

    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })

  it('rejects a malformed email', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await user.type(screen.getByLabelText(/email/i), 'not-an-email')
    await solveCaptcha(user)
    await user.click(screen.getByRole('button', { name: /confirm booking/i }))

    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('requires the captcha before submitting', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
    await user.click(screen.getByRole('button', { name: /confirm booking/i }))

    expect(onSubmit).not.toHaveBeenCalled()
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })

  it('submits the trimmed values with the slot and captcha token', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.type(screen.getByLabelText(/your name/i), '  Ada  ')
    await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
    await user.type(screen.getByLabelText(/know/i), 'About the API')
    await solveCaptcha(user)
    await user.click(screen.getByRole('button', { name: /confirm booking/i }))

    expect(onSubmit).toHaveBeenCalledWith({
      startAt: SLOT.start,
      name: 'Ada',
      email: 'ada@example.com',
      note: 'About the API',
      turnstileToken: 'tok',
    })
  })

  it('leaves the note out when it is empty', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderForm()

    await user.type(screen.getByLabelText(/your name/i), 'Ada')
    await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
    await solveCaptcha(user)
    await user.click(screen.getByRole('button', { name: /confirm booking/i }))

    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ note: undefined, name: 'Ada' }))
  })

  it('submits without a captcha when Turnstile is not configured', async () => {
    vi.mocked(useCaptchaEnabled).mockReturnValue(false)
    vi.mocked(useTurnstileSiteKey).mockReturnValue('')
    try {
      const user = userEvent.setup()
      const { onSubmit } = renderForm()

      expect(screen.queryByRole('button', { name: 'mock turnstile' })).not.toBeInTheDocument()
      await user.type(screen.getByLabelText(/your name/i), 'Ada')
      await user.type(screen.getByLabelText(/email/i), 'ada@example.com')
      await user.click(screen.getByRole('button', { name: /confirm booking/i }))

      expect(onSubmit).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Ada', email: 'ada@example.com', turnstileToken: undefined }),
      )
    } finally {
      vi.mocked(useCaptchaEnabled).mockReturnValue(true)
      vi.mocked(useTurnstileSiteKey).mockReturnValue('site-key')
    }
  })
})
