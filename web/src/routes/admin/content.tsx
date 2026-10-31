import { createFileRoute } from '@tanstack/react-router'
import { ContentModeration } from '#/components/admin/ContentModeration'

/**
 * Abuse-report takedowns (#48): find a poll or booking page and suspend it or lift the
 * suspension. Staff-gated by the parent `/admin` layout for navigation and by `RequireStaff` on
 * every request underneath.
 */
export const Route = createFileRoute('/admin/content')({
  component: ContentModeration,
})
