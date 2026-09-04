import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { ErrorCard } from '#/components/layout/ErrorCard'
import { ApiError } from '#/api/client'
import { m } from '#/lib/i18n'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

/** `ErrorCard`'s "go home" button is a `Link`, so it needs a router around it. */
async function renderErrorCard(error: unknown) {
  const rootRoute = createRootRoute({ component: () => <ErrorCard error={error} /> })
  const router = createRouter({ routeTree: rootRoute, context: { session: null } })
  render(<RouterProvider router={router} />)
  await screen.findByText(m.error_title())
}

describe('ErrorCard', () => {
  it('never renders the raw text of an unrecognized error', async () => {
    const leak = 'pq: column "email" of relation "user" does not exist'

    await renderErrorCard(new Error(leak))

    expect(screen.queryByText(leak)).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(leak)
  })

  it('renders the translated message for a recognized API error code', async () => {
    await renderErrorCard(new ApiError('rate_limited', 'slow down, 12 req/s from 10.0.0.4', 429))

    expect(screen.getByText(m.error_rate_limited())).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('10.0.0.4')
  })

  it('renders no detail line at all for an API error code it has no message for', async () => {
    await renderErrorCard(new ApiError('slug_taken', 'slug "standup" is taken', 409))

    expect(document.body.textContent).not.toContain('standup')
  })

  it('logs the full error so the detail survives in the console', async () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const error = new Error('pq: relation "polls" does not exist')

    await renderErrorCard(error)

    expect(spy).toHaveBeenCalledWith('[error-boundary]', error)
  })
})
