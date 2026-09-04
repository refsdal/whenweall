import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { FailedJobsTable } from '#/components/admin/FailedJobsTable'
import type { FailedJobView } from '#/api/types'

const server = setupServer()
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
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

const retryable: FailedJobView = {
  id: 'j1',
  kind: 'mail:send',
  attempts: 10,
  lastError: 'smtp: connection refused',
  runAt: '2026-09-01T10:00:00.000Z',
  payloadExpired: false,
}
const purged: FailedJobView = {
  id: 'j2',
  kind: 'mail:booking',
  attempts: 10,
  lastError: 'smtp: 550 mailbox unavailable',
  runAt: '2026-08-01T10:00:00.000Z',
  payloadExpired: true,
}

describe('FailedJobsTable', () => {
  it('lists kind, attempts and the last error for every dead job', () => {
    render(<FailedJobsTable jobs={[retryable, purged]} onRetried={vi.fn()} />)

    expect(screen.getByText('mail:send')).toBeInTheDocument()
    expect(screen.getByText('mail:booking')).toBeInTheDocument()
    expect(screen.getByText('smtp: connection refused')).toBeInTheDocument()
    expect(screen.getByText('smtp: 550 mailbox unavailable')).toBeInTheDocument()
    expect(screen.getAllByText('10')).toHaveLength(2)
  })

  it('retries a job and asks the page to refetch', async () => {
    let hit = false
    server.use(
      http.post('/api/v1/admin/jobs/j1/retry', () => {
        hit = true
        return HttpResponse.json({ ok: true })
      }),
    )
    const onRetried = vi.fn()
    render(<FailedJobsTable jobs={[retryable]} onRetried={onRetried} />)

    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() => expect(onRetried).toHaveBeenCalledOnce())
    expect(hit).toBe(true)
    expect(toast.success).toHaveBeenCalledWith('Job queued to run again.')
  })

  // internal/jobs/housekeeping.go's deadletter:sweep nulls a dead mail job's payload after 24h and
  // the backend answers 409 payload_expired to a retry of it — so the table must not offer one.
  it('offers no retry for a purged payload and says why', () => {
    render(<FailedJobsTable jobs={[retryable, purged]} onRetried={vi.fn()} />)

    expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1)
    expect(screen.getByText(/payload purged/i)).toBeInTheDocument()
  })

  it('shows an error toast and does not refetch when the backend refuses', async () => {
    server.use(
      http.post('/api/v1/admin/jobs/j1/retry', () =>
        HttpResponse.json(
          { error: { code: 'conflict', message: 'job is not dead-lettered' } },
          { status: 409 },
        ),
      ),
    )
    const onRetried = vi.fn()
    render(<FailedJobsTable jobs={[retryable]} onRetried={onRetried} />)

    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(expect.stringContaining('job is not dead-lettered')),
    )
    expect(onRetried).not.toHaveBeenCalled()
  })

  it('says so when nothing has failed', () => {
    render(<FailedJobsTable jobs={[]} onRetried={vi.fn()} />)

    expect(screen.getByText(/no failed jobs/i)).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})
