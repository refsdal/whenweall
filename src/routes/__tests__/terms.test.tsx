import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { TermsPage } from '#/routes/terms'

afterEach(() => cleanup())

describe('TermsPage', () => {
  it('renders the terms title, updated date, and operating entity', () => {
    render(<TermsPage />)

    expect(screen.getByRole('heading', { level: 1, name: 'Terms of Service' })).toBeInTheDocument()
    expect(screen.getByText('Updated 26 August 2026')).toBeInTheDocument()
    expect(screen.getAllByText(/Refsdal Holding AS/).length).toBeGreaterThan(0)
    expect(screen.getByText(/932 516 470/)).toBeInTheDocument()
  })

  it('wires up every content section', () => {
    render(<TermsPage />)

    for (const heading of [
      'The service',
      'Accounts and organizations',
      'Acceptable use',
      'Your content',
      'Availability and warranty',
      'Liability',
      'Governing law',
      'Changes to these terms',
    ]) {
      expect(screen.getByRole('heading', { level: 2, name: heading })).toBeInTheDocument()
    }
  })

  it('describes Premium billing via Stripe in NOK', () => {
    render(<TermsPage />)

    expect(screen.getByText(/Stripe/)).toBeInTheDocument()
    expect(screen.getByText(/NOK/)).toBeInTheDocument()
  })
})
