import { useState } from 'react'
import { toast } from 'sonner'
import { retryJob } from '#/api/admin'
import { ApiError } from '#/api/client'
import type { FailedJobView } from '#/api/types'
import { Button } from '#/components/ui/button'
import { m } from '#/lib/i18n'

/**
 * The dead-letter queue (spec §5: a parked job must "surface in the admin console" — the
 * `fix/mail-failure-visibility` lesson). One row per job that exhausted its retry budget, with
 * the failure text the worker recorded; Retry calls `POST /api/v1/admin/jobs/{id}/retry`, which
 * re-queues the job to run immediately and writes a `job.retry` audit row.
 *
 * A row whose payload the `deadletter:sweep` housekeeping job has purged (`payloadExpired`) can
 * never be retried — the backend would answer `409 payload_expired` — so it gets an explanation
 * instead of a button. Payloads themselves are never in this view at all: they may hold
 * recipient addresses and tokens (see `FailedJobView` in internal/admin/handlers.go).
 */
export function FailedJobsTable({
  jobs,
  onRetried,
}: {
  jobs: FailedJobView[]
  onRetried: () => Promise<void> | void
}) {
  const [busyId, setBusyId] = useState<string | null>(null)

  async function retry(id: string) {
    setBusyId(id)
    try {
      await retryJob(id)
      toast.success(m.admin_jobs_toast_retried())
      await onRetried()
    } catch (error) {
      const message = error instanceof ApiError ? error.message : String(error)
      toast.error(m.admin_jobs_retry_failed({ message }))
    } finally {
      setBusyId(null)
    }
  }

  if (jobs.length === 0) {
    return <p className="text-sm text-muted-foreground">{m.admin_jobs_empty()}</p>
  }

  return (
    <div className="overflow-x-auto rounded-lg border">
      <table className="w-full text-sm">
        <thead className="border-b text-left text-muted-foreground">
          <tr>
            <th className="p-3 font-medium">{m.admin_jobs_col_kind()}</th>
            <th className="p-3 font-medium">{m.admin_jobs_col_attempts()}</th>
            <th className="p-3 font-medium">{m.admin_jobs_col_error()}</th>
            <th className="p-3 font-medium">{m.admin_jobs_col_run_at()}</th>
            <th className="p-3 font-medium">
              <span className="sr-only">{m.admin_jobs_col_actions()}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((job) => (
            <tr key={job.id} className="border-b align-top last:border-0">
              <td className="p-3 font-mono text-xs">{job.kind}</td>
              <td className="p-3 tabular-nums">{job.attempts}</td>
              <td className="max-w-md p-3 break-words text-muted-foreground">
                {job.lastError ?? '—'}
              </td>
              <td className="p-3 whitespace-nowrap text-muted-foreground">
                {new Date(job.runAt).toLocaleString()}
              </td>
              <td className="p-3 text-right">
                {job.payloadExpired ? (
                  <span className="text-xs text-muted-foreground italic">
                    {m.admin_jobs_payload_expired()}
                  </span>
                ) : (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={busyId === job.id}
                    onClick={() => void retry(job.id)}
                  >
                    {m.admin_jobs_retry()}
                  </Button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
