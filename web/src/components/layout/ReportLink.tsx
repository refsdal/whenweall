import { Flag } from 'lucide-react'
import { appConfig } from '#/app.config'
import { m } from '#/lib/i18n'

/**
 * "Report abuse" on a public poll, sign-up sheet or booking page (#48): a plain `mailto:` to the
 * support address with the page's kind and id already in the subject, so a report names what it
 * is about without the reporter having to work it out. No account needed — the person best
 * placed to report a scam link is someone who was sent it and never signed up. Staff act on it
 * with the suspension flag described in docs/abuse-reports.md.
 */
export function reportHref(kind: 'poll' | 'booking page', id: string, url: string): string {
  const subject = encodeURIComponent(`Report: ${kind} ${id}`)
  const body = encodeURIComponent(`${url}\n\n`)
  return `mailto:${appConfig.supportEmail}?subject=${subject}&body=${body}`
}

export function ReportLink({
  kind,
  id,
  url,
}: {
  kind: 'poll' | 'booking page'
  id: string
  url: string
}) {
  return (
    <div className="flex justify-center">
      <a
        href={reportHref(kind, id, url)}
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
      >
        <Flag aria-hidden="true" className="size-3" />
        {m.report_abuse()}
      </a>
    </div>
  )
}
