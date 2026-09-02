import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { ManageBooking } from '#/components/booking/ManageBooking'
import type { BookingForManage } from '#/server/bookings/viewmodel'

// `useServerFn` calls `useRouter()`, which throws outside a `<RouterProvider>`; the booking
// actions themselves aren't exercised here, so the hook is stood in with an identity function.
vi.mock('@tanstack/react-start', () => ({ useServerFn: (fn: unknown) => fn }))
vi.mock('#/server/bookings/bookings.functions', () => ({
  cancelBooking: vi.fn(),
  rescheduleBooking: vi.fn(),
  getPublicAvailability: vi.fn(),
}))

afterEach(() => {
  cleanup()
  window.localStorage.clear()
})

const NOW = '2026-08-20T00:00:00.000Z'

function makeBooking(overrides: Partial<BookingForManage['page']> = {}): BookingForManage {
  return {
    id: 'bk_1',
    pageId: 'pg_1',
    startAt: '2026-09-15T07:00:00.000Z',
    endAt: '2026-09-15T07:30:00.000Z',
    visitorName: 'Ada',
    visitorEmail: 'ada@example.com',
    visitorNote: null,
    visitorTimezone: 'Europe/Oslo',
    visitorLocale: 'en',
    status: 'confirmed',
    cancelledBy: null,
    createdAt: NOW,
    page: {
      id: 'pg_1',
      handle: 'ada',
      slug: 'intro-call',
      title: 'Intro call',
      location: null,
      timezone: 'Europe/Oslo',
      slotDurationMin: 30,
      owner: { name: 'Grace' },
      ...overrides,
    },
  }
}

function renderManage(booking: BookingForManage, now = NOW) {
  render(<ManageBooking booking={booking} now={now} token="tok" onChanged={vi.fn()} />)
}

describe('ManageBooking', () => {
  it('offers reschedule and cancel for an upcoming booking', () => {
    renderManage(makeBooking())

    expect(screen.getByRole('button', { name: /reschedule/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel booking/i })).toBeInTheDocument()
  })

  it('hides reschedule when the organiser has no handle, since there is no page to book on', () => {
    renderManage(makeBooking({ handle: null }))

    expect(screen.queryByRole('button', { name: /reschedule/i })).not.toBeInTheDocument()
    // Cancelling never needs the public page, so it stays.
    expect(screen.getByRole('button', { name: /cancel booking/i })).toBeInTheDocument()
  })

  it('offers neither once the booking is in the past', () => {
    renderManage(makeBooking(), '2026-10-01T00:00:00.000Z')

    expect(screen.queryByRole('button', { name: /reschedule/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /cancel booking/i })).not.toBeInTheDocument()
  })
})
