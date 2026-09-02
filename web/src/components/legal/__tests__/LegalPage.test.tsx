import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { LegalPage } from '#/components/legal/LegalPage'

afterEach(() => cleanup())

describe('LegalPage', () => {
  it('renders the title, updated line, and intro', () => {
    render(
      <LegalPage
        title="Privacy Policy"
        updated="Updated 26 August 2026"
        intro="This policy explains things."
        sections={[]}
      />,
    )

    expect(screen.getByRole('heading', { level: 1, name: 'Privacy Policy' })).toBeInTheDocument()
    expect(screen.getByText('Updated 26 August 2026')).toBeInTheDocument()
    expect(screen.getByText('This policy explains things.')).toBeInTheDocument()
  })

  it('renders each section heading and its body', () => {
    render(
      <LegalPage
        title="Terms"
        updated="Updated 26 August 2026"
        intro="Intro"
        sections={[
          { title: 'The service', body: 'We provide scheduling tools.' },
          { title: 'Accounts', body: 'You must be 13 or older.' },
        ]}
      />,
    )

    expect(screen.getByRole('heading', { level: 2, name: 'The service' })).toBeInTheDocument()
    expect(screen.getByText('We provide scheduling tools.')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 2, name: 'Accounts' })).toBeInTheDocument()
    expect(screen.getByText('You must be 13 or older.')).toBeInTheDocument()
  })

  it('splits a section body into separate paragraphs on double newlines', () => {
    render(
      <LegalPage
        title="Terms"
        updated="Updated 26 August 2026"
        intro="Intro"
        sections={[{ title: 'Content', body: 'First paragraph.\n\nSecond paragraph.' }]}
      />,
    )

    const first = screen.getByText('First paragraph.')
    const second = screen.getByText('Second paragraph.')
    expect(first.tagName).toBe('P')
    expect(second.tagName).toBe('P')
    expect(first).not.toBe(second)
  })

  it('renders children after the sections', () => {
    render(
      <LegalPage title="Terms" updated="Updated" intro="Intro" sections={[]}>
        <p>Contact us</p>
      </LegalPage>,
    )

    expect(screen.getByText('Contact us')).toBeInTheDocument()
  })
})
