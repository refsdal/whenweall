import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TurnstileField } from '#/components/auth/TurnstileField'

// The real widget loads Cloudflare's script and renders an iframe challenge — none of that runs
// in jsdom. Stand in with a button that fires the same callback props the real component takes,
// so the test proves TurnstileField wires `onToken` to the library correctly.
vi.mock('@marsidev/react-turnstile', () => ({
  Turnstile: ({
    onSuccess,
    options,
  }: {
    onSuccess: (token: string) => void
    options?: { size?: string }
  }) => (
    <button type="button" data-size={options?.size ?? ''} onClick={() => onSuccess('tok')}>
      mock turnstile
    </button>
  ),
}))

afterEach(() => cleanup())

describe('TurnstileField', () => {
  it('renders the widget container and reports a token via onToken', async () => {
    const user = userEvent.setup()
    const onToken = vi.fn()
    render(<TurnstileField onToken={onToken} />)

    const widget = screen.getByRole('button', { name: 'mock turnstile' })
    await user.click(widget)

    expect(onToken).toHaveBeenCalledWith('tok')
  })

  it('does not force a fixed widget size, so an invisible-mode widget leaves no empty box', () => {
    render(<TurnstileField onToken={vi.fn()} />)

    // `@marsidev/react-turnstile` inline-styles its container to a fixed height/min-width
    // whenever `options.size` is set (e.g. 'flexible' or 'normal'). Leaving it unset is what lets
    // the container collapse to nothing when the production (invisible) widget renders no UI.
    expect(screen.getByRole('button', { name: 'mock turnstile' })).toHaveAttribute('data-size', '')
  })
})
