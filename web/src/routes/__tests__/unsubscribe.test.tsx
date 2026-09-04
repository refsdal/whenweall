import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { UnsubscribePanel } from '#/routes/unsubscribe'
import { m } from '#/lib/i18n'

const TOKEN = 'YWRhQGV4YW1wbGUuY29t.c2ln'

let calls: { method: string; url: string }[] = []

beforeEach(() => {
  calls = []
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

/** Stubs fetch with one queued response per call, recording method and URL. */
function stubFetch(...responses: { status: number; body: unknown }[]) {
  let i = 0
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, init?: RequestInit) => {
      calls.push({ method: init?.method ?? 'GET', url: String(url) })
      const next = responses[Math.min(i++, responses.length - 1)]!
      return Promise.resolve(
        new Response(JSON.stringify(next.body), {
          status: next.status,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    }),
  )
}

async function renderPanel(token: string | undefined) {
  const rootRoute = createRootRoute({ component: () => <UnsubscribePanel token={token} /> })
  const router = createRouter({ routeTree: rootRoute, context: { session: null } })
  render(<RouterProvider router={router} />)
  await screen.findByRole('heading')
}

describe('UnsubscribePanel', () => {
  // Nothing happens until a person clicks. A link scanner or a mail client prefetching the
  // page must not silence someone who never chose to be silenced.
  it('does not unsubscribe on load', async () => {
    stubFetch({ status: 200, body: { status: 'unsubscribed', email: 'ada@example.com' } })

    await renderPanel(TOKEN)

    expect(screen.getByRole('button', { name: m.unsub_submit() })).toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })

  it('unsubscribes on click and confirms', async () => {
    stubFetch({ status: 200, body: { status: 'unsubscribed', email: 'ada@example.com' } })
    await renderPanel(TOKEN)

    await userEvent.click(screen.getByRole('button', { name: m.unsub_submit() }))

    await waitFor(() => expect(screen.getByText(m.unsub_done_title())).toBeInTheDocument())
    expect(calls).toHaveLength(1)
    expect(calls[0]!.method).toBe('POST')
    expect(calls[0]!.url).toContain(`token=${encodeURIComponent(TOKEN)}`)
    expect(screen.getByText(m.unsub_done_body({ email: 'ada@example.com' }))).toBeInTheDocument()
  })

  // One accidental click, on a link that is someone's only credential, must not be permanent.
  it('offers a resubscribe that undoes it', async () => {
    stubFetch(
      { status: 200, body: { status: 'unsubscribed', email: 'ada@example.com' } },
      { status: 200, body: { status: 'subscribed', email: 'ada@example.com' } },
    )
    await renderPanel(TOKEN)

    await userEvent.click(screen.getByRole('button', { name: m.unsub_submit() }))
    await waitFor(() => expect(screen.getByText(m.unsub_done_title())).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: m.unsub_undo() }))

    await waitFor(() => expect(screen.getByText(m.unsub_undone_title())).toBeInTheDocument())
    expect(calls[1]!.method).toBe('DELETE')
  })

  it('explains a rejected token instead of pretending it worked', async () => {
    stubFetch({ status: 400, body: { error: { code: 'invalid_token', message: 'nope' } } })
    await renderPanel(TOKEN)

    await userEvent.click(screen.getByRole('button', { name: m.unsub_submit() }))

    await waitFor(() => expect(screen.getByText(m.unsub_invalid_title())).toBeInTheDocument())
  })

  it('shows the invalid-link card when there is no token at all', async () => {
    stubFetch({ status: 200, body: {} })

    await renderPanel(undefined)

    expect(screen.getByText(m.unsub_invalid_title())).toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })
})
