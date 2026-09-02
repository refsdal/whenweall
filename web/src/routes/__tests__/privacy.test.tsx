import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { PrivacyPage } from '#/routes/privacy'

afterEach(() => cleanup())

describe('PrivacyPage', () => {
  it('renders the privacy title, updated date, and controller contact details', () => {
    render(<PrivacyPage />)

    expect(screen.getByRole('heading', { level: 1, name: 'Privacy Policy' })).toBeInTheDocument()
    expect(screen.getByText('Updated 28 August 2026')).toBeInTheDocument()
    expect(screen.getByText(/Refsdal Holding AS/)).toBeInTheDocument()
    expect(screen.getByText(/932 516 470/)).toBeInTheDocument()
    expect(screen.getAllByText(/hello@whenweall\.com/).length).toBeGreaterThan(0)
  })

  it('wires up every content section', () => {
    render(<PrivacyPage />)

    for (const heading of [
      'Who is responsible for your data',
      'What we store',
      'Who else processes your data',
      'Why we process your data, and on what basis',
      'How long we keep your data',
      'What participants can see',
      'Cookies',
      'Your rights',
    ]) {
      expect(screen.getByRole('heading', { level: 2, name: heading })).toBeInTheDocument()
    }
  })

  it('names the processors used by the service', () => {
    render(<PrivacyPage />)

    expect(screen.getAllByText(/Stripe/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/Cloudflare/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/Google/).length).toBeGreaterThan(0)
  })
})
