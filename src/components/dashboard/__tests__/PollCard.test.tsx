import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { PollCard } from '#/components/dashboard/PollCard'
import type { PollSummary } from '#/server/polls/viewmodel'

afterEach(() => cleanup())

function makeSummary(overrides: Partial<PollSummary> = {}): PollSummary {
  return {
    id: 'abcdefghijkl',
    title: 'Dinner with the crew',
    type: 'datetime',
    status: 'open',
    deadlineAt: null,
    participantCount: 3,
    claimCount: 3,
    createdAt: '2026-08-01T10:00:00.000Z',
    updatedAt: '2026-08-01T10:00:00.000Z',
    ...overrides,
  }
}

/** `Link` needs a router in context, so the card is rendered inside a minimal one. */
async function renderCard(props: Omit<Parameters<typeof PollCard>[0], never>) {
  const rootRoute = createRootRoute({
    component: () => <PollCard {...props} />,
  })
  const router = createRouter({ routeTree: rootRoute })
  render(<RouterProvider router={router} />)
  await screen.findByText(props.poll.title)
}

describe('PollCard', () => {
  it('renders the title, status and participant count', async () => {
    await renderCard({ poll: makeSummary(), onDuplicate: vi.fn(), onDelete: vi.fn() })

    expect(screen.getByText('Dinner with the crew')).toBeInTheDocument()
    expect(screen.getByTestId('poll-card-status')).toHaveTextContent('Open')
    expect(screen.getByText(/3 people/)).toBeInTheDocument()
  })

  it('shows a singular participant count', async () => {
    await renderCard({
      poll: makeSummary({ participantCount: 1 }),
      onDuplicate: vi.fn(),
      onDelete: vi.fn(),
    })

    expect(screen.getByText(/1 person/)).toBeInTheDocument()
  })

  it('counts claims, not participants, for a sign-up sheet', async () => {
    await renderCard({
      poll: makeSummary({ type: 'signup', participantCount: 2, claimCount: 5 }),
      onDuplicate: vi.fn(),
      onDelete: vi.fn(),
    })

    expect(screen.getByText(/5 sign-ups/)).toBeInTheDocument()
  })

  it('shows the finalized status', async () => {
    await renderCard({
      poll: makeSummary({ status: 'finalized' }),
      onDuplicate: vi.fn(),
      onDelete: vi.fn(),
    })

    expect(screen.getByTestId('poll-card-status')).toHaveTextContent('Decided')
  })

  it('calls onDuplicate when duplicate is clicked', async () => {
    const user = userEvent.setup()
    const onDuplicate = vi.fn()
    await renderCard({ poll: makeSummary(), onDuplicate, onDelete: vi.fn() })

    await user.click(screen.getByRole('button', { name: /duplicate/i }))

    expect(onDuplicate).toHaveBeenCalledTimes(1)
  })

  it('does not call onDelete until the confirm dialog is accepted', async () => {
    const user = userEvent.setup()
    const onDelete = vi.fn()
    await renderCard({ poll: makeSummary(), onDuplicate: vi.fn(), onDelete })

    await user.click(screen.getByRole('button', { name: /delete poll/i }))
    expect(onDelete).not.toHaveBeenCalled()

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /^delete$/i }))

    expect(onDelete).toHaveBeenCalledTimes(1)
  })
})
