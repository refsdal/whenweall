import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { Footer } from '#/components/layout/Footer'

afterEach(() => cleanup())

/** `LocaleSwitcher` reads `session` off the root route context, and `Link` needs a router. */
async function renderFooter() {
  const rootRoute = createRootRoute({ component: () => <Footer /> })
  const router = createRouter({ routeTree: rootRoute, context: { session: null } })
  render(<RouterProvider router={router} />)
  await screen.findByRole('contentinfo')
}

describe('Footer', () => {
  it('links to the privacy policy and terms pages', async () => {
    await renderFooter()

    const privacyLink = screen.getByRole('link', { name: 'Privacy' })
    expect(privacyLink).toHaveAttribute('href', '/privacy')

    const termsLink = screen.getByRole('link', { name: 'Terms' })
    expect(termsLink).toHaveAttribute('href', '/terms')
  })

  it('still shows the rights line and locale switcher', async () => {
    await renderFooter()

    expect(screen.getByText(new RegExp(`© ${new Date().getFullYear()}`))).toBeInTheDocument()
    expect(screen.getByRole('group')).toBeInTheDocument()
  })
})
