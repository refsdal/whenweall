import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { BookingConfirmed } from '#/components/booking/BookingConfirmed'

afterEach(() => cleanup())

/** `Link` needs a router in context, so the card is rendered inside a minimal one (the same
 * shape `components/dashboard/__tests__/PollCard.test.tsx` uses). */
async function renderConfirmed() {
  const rootRoute = createRootRoute({
    component: () => (
      <BookingConfirmed
        bookingId="bk_1"
        manageToken="tok/with+chars"
        title="Intro call"
        location={null}
        slot={{ start: '2026-09-15T07:00:00.000Z', end: '2026-09-15T07:30:00.000Z' }}
        timeZone="Europe/Oslo"
        email="ada@example.com"
        onBookAnother={vi.fn()}
      />
    ),
  })
  const router = createRouter({ routeTree: rootRoute })
  render(<RouterProvider router={router} />)
  await screen.findByTestId('booking-confirmed')
}

describe('BookingConfirmed', () => {
  it('links "Add to calendar" at the Go .ics endpoint, carrying the manage token', async () => {
    await renderConfirmed()

    const link = screen.getByRole('link', { name: /add to calendar/i })
    expect(link).toHaveAttribute(
      'href',
      '/api/v1/bookings/bk_1/calendar.ics?t=' + encodeURIComponent('tok/with+chars'),
    )
  })
})
