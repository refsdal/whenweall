import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { ReportLink, reportHref } from '#/components/layout/ReportLink'

afterEach(() => cleanup())

describe('ReportLink', () => {
  it('mails the support address with the kind and id in the subject and the link in the body', () => {
    const href = reportHref('poll', 'abc123', 'https://whenweall.com/p/abc123')
    const url = new URL(href)
    expect(url.protocol).toBe('mailto:')
    expect(url.pathname).toBe('hello@whenweall.com')
    expect(url.searchParams.get('subject')).toBe('Report: poll abc123')
    expect(url.searchParams.get('body')).toContain('https://whenweall.com/p/abc123')
  })

  it('renders as a plain link anyone can follow, no account needed', () => {
    render(<ReportLink kind="booking page" id="p1" url="https://whenweall.com/book/a/b" />)
    const link = screen.getByRole('link', { name: 'Report abuse' })
    expect(link.getAttribute('href')).toBe(
      reportHref('booking page', 'p1', 'https://whenweall.com/book/a/b'),
    )
  })
})
