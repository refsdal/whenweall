import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { NotificationGrid } from '#/components/notifications/NotificationGrid'
import type { NotificationEvent } from '#/lib/notifications'
import * as m from '#/paraglide/messages'

afterEach(() => cleanup())

const EVENTS: readonly NotificationEvent[] = [
  'response.created',
  'comment.created',
  'poll.finalized',
]

/**
 * Nothing in this codebase delivers a web push notification: there is no service worker, no VAPID
 * key, no subscribe call and no send path. `ChannelPrefs` still carries a `push` boolean because
 * it is the shape of a stored jsonb grid (dropping it would need a data migration for no user-
 * visible gain), but a checkbox for it would be a control that silently does nothing — the one
 * thing the old billing-era bug did, where Premium orgs could switch push on and receive nothing.
 *
 * This is the guard on that: the grid offers exactly one channel per event.
 */
describe('NotificationGrid', () => {
  it('offers exactly one channel — email — and no control for undelivered push', () => {
    render(
      <NotificationGrid events={EVENTS} value={null} defaults={null} onChange={() => {}} />,
    )

    expect(screen.getByText(m.notif_channel_email())).toBeInTheDocument()
    expect(screen.getAllByRole('checkbox')).toHaveLength(EVENTS.length)
    for (const box of screen.getAllByRole('checkbox')) {
      expect(box.getAttribute('aria-label')).toContain(m.notif_channel_email())
    }
  })

  it('writes a fully resolved row so the untouched channel keeps its value', async () => {
    const onChange = vi.fn()
    render(
      <NotificationGrid events={EVENTS} value={null} defaults={null} onChange={onChange} />,
    )

    // 'response.created' defaults to email on; clicking turns it off.
    await userEvent.click(screen.getAllByRole('checkbox')[0]!)

    expect(onChange).toHaveBeenCalledWith({
      'response.created': { email: false, push: true },
    })
  })
})
