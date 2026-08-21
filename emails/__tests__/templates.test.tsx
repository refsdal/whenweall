import { describe, expect, it } from 'vitest'
import {
  renderClosed,
  renderDigest,
  renderFinalized,
  renderResetPassword,
  renderVerifyEmail,
} from '#/server/mailer/templates'

describe('renderVerifyEmail', () => {
  it('renders english', async () => {
    const { subject, html, text } = await renderVerifyEmail({
      name: 'Ada',
      url: 'https://x/verify?token=1',
      locale: 'en',
    })

    expect(subject).toContain('Verify')
    expect(html).toContain('https://x/verify?token=1')
    expect(html).toContain('Ada')
    expect(text).toContain('https://x/verify?token=1')
  })

  it('renders norwegian', async () => {
    const { subject } = await renderVerifyEmail({
      name: 'Ada',
      url: 'https://x/verify?token=1',
      locale: 'nb',
    })

    expect(subject).toContain('Bekreft')
  })
})

describe('renderResetPassword', () => {
  it('renders with url and name', async () => {
    const { subject, html, text } = await renderResetPassword({
      name: 'Ada',
      url: 'https://x/reset?token=1',
      locale: 'en',
    })

    expect(subject.length).toBeGreaterThan(0)
    expect(html).toContain('https://x/reset?token=1')
    expect(html).toContain('Ada')
    expect(text).toContain('https://x/reset?token=1')
  })
})

describe('renderDigest', () => {
  it('lists voter names and includes the poll title in the subject', async () => {
    const { subject, html } = await renderDigest({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      newVoters: ['Ada', 'Grace', 'Rosalind'],
      newComments: 1,
      locale: 'en',
    })

    expect(subject).toContain('Team sync')
    expect(html).toContain('Ada')
    expect(html).toContain('Grace')
    expect(html).toContain('Rosalind')
    expect(html).toContain('https://x/p/abc')
  })
})

describe('renderFinalized', () => {
  it('includes the winning option label', async () => {
    const { html, subject } = await renderFinalized({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      optionLabel: 'Wed 10:00 – 11:00',
      recipientName: 'Ada',
      locale: 'en',
    })

    expect(html).toContain('Wed 10:00 – 11:00')
    expect(subject).toContain('Team sync')
  })
})

describe('renderClosed', () => {
  it('includes the poll title and url', async () => {
    const { html, subject } = await renderClosed({
      pollTitle: 'Team sync',
      pollUrl: 'https://x/p/abc',
      locale: 'en',
    })

    expect(subject).toContain('Team sync')
    expect(html).toContain('https://x/p/abc')
  })
})
