import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { ContentModeration } from '#/components/admin/ContentModeration'
import type { AdminContentItem } from '#/api/types'

// Real `api()` client against msw, like UserActions.test.tsx: the wire contract is under test.
const server = setupServer()
beforeAll(() => server.listen({ onUnhandledFrame: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
})
afterAll(() => server.close())

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast }))
beforeEach(() => {
  toast.success.mockReset()
  toast.error.mockReset()
})

const user = () => userEvent.setup({ pointerEventsCheck: 0 })

function item(overrides: Partial<AdminContentItem> = {}): AdminContentItem {
  return {
    kind: 'poll',
    id: 'scam-poll',
    title: 'Claim your prize',
    pollType: 'dates',
    organizationName: 'Reported Org',
    organizationSlug: 'reported-org',
    creatorEmail: 'owner@example.com',
    publicPath: '/p/scam-poll',
    createdAt: '2026-10-01T10:00:00.000Z',
    deletedAt: null,
    suspendedAt: null,
    ...overrides,
  }
}

describe('ContentModeration', () => {
  it('finds an item from a pasted link and suspends it with a reason', async () => {
    let suspended = false
    let sentBody: unknown = null
    let lookedUp = ''
    server.use(
      http.get('*/api/v1/admin/content', ({ request }) => {
        lookedUp = new URL(request.url).searchParams.get('q') ?? ''
        return HttpResponse.json(item({ suspendedAt: suspended ? '2026-10-31T22:00:00.000Z' : null }))
      }),
      http.post('*/api/v1/admin/content/poll/scam-poll/suspend', async ({ request }) => {
        sentBody = await request.json()
        suspended = true
        return HttpResponse.json({ ok: true })
      }),
    )
    const u = user()
    render(<ContentModeration />)

    await u.type(screen.getByLabelText(/link or id/i), 'https://whenweall.com/p/scam-poll')
    await u.click(screen.getByRole('button', { name: /^find$/i }))

    expect(await screen.findByRole('heading', { name: 'Claim your prize' })).toBeInTheDocument()
    expect(lookedUp).toBe('https://whenweall.com/p/scam-poll')
    expect(screen.getByText('owner@example.com')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /open public page/i })).toHaveAttribute('href', '/p/scam-poll')

    await u.click(screen.getByRole('button', { name: /^suspend$/i }))
    await u.type(screen.getByLabelText(/why are you doing this/i), 'phishing report')
    const confirm = screen.getAllByRole('button', { name: /^suspend$/i }).at(-1)!
    await u.click(confirm)

    await waitFor(() => expect(sentBody).toEqual({ reason: 'phishing report' }))
    expect(toast.success).toHaveBeenCalledWith('Suspended.')
    // The card re-reads the server's state: now suspended, offering to lift it.
    expect(await screen.findByText('Suspended')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /lift suspension/i })).toBeInTheDocument()
  })

  it('says so when nothing matches', async () => {
    server.use(
      http.get('*/api/v1/admin/content', () =>
        HttpResponse.json({ error: { code: 'not_found', message: 'not found' } }, { status: 404 }),
      ),
    )
    const u = user()
    render(<ContentModeration />)
    await u.type(screen.getByLabelText(/link or id/i), 'nothing')
    await u.click(screen.getByRole('button', { name: /^find$/i }))
    expect(await screen.findByText(/no poll or booking page matches/i)).toBeInTheDocument()
  })

  it('labels a suspended, owner-deleted sign-up sheet as such', async () => {
    server.use(
      http.get('*/api/v1/admin/content', () =>
        HttpResponse.json(
          item({
            pollType: 'signup',
            creatorEmail: null,
            suspendedAt: '2026-10-31T22:00:00.000Z',
            deletedAt: '2026-10-31T23:00:00.000Z',
          }),
        ),
      ),
    )
    const u = user()
    render(<ContentModeration />)
    await u.type(screen.getByLabelText(/link or id/i), 'scam-poll')
    await u.click(screen.getByRole('button', { name: /^find$/i }))
    expect(await screen.findByText('Sign-up sheet')).toBeInTheDocument()
    expect(screen.getByText('Deleted by owner')).toBeInTheDocument()
    expect(screen.getByText('account deleted')).toBeInTheDocument()
  })
})
