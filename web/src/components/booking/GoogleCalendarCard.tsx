import { useEffect, useState } from 'react'
import { useServerFn } from '@tanstack/react-start'
import { CalendarCheck, CalendarSync } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import { Label } from '#/components/ui/label'
import { Switch } from '#/components/ui/switch'
import { m } from '#/lib/i18n'
import { authClient } from '#/server/auth/client'
import {
  disconnectGoogleCalendar,
  getGoogleCalendarStatus,
} from '#/server/bookings/pages.functions'

/**
 * Read-only busy times plus the ability to write the booking back as an event — the two scopes
 * `server/google/calendar.ts` actually uses, and nothing else.
 */
const CALENDAR_SCOPES = [
  'https://www.googleapis.com/auth/calendar.readonly',
  'https://www.googleapis.com/auth/calendar.events',
]

/**
 * The Google Calendar section of the page editor: whether this account has a usable calendar
 * connection, a button to grant one (incremental consent via Better-Auth's `linkSocial`), and the
 * per-page sync switch — which can only be on while a connection exists.
 */
export function GoogleCalendarCard({
  googleSync,
  googleEnabled,
  callbackURL,
  onSyncChange,
}: {
  googleSync: boolean
  googleEnabled: boolean
  /** Where Google sends the organiser back to — the editor they started from. */
  callbackURL: string
  onSyncChange: (next: boolean) => void
}) {
  const statusFn = useServerFn(getGoogleCalendarStatus)
  const disconnectFn = useServerFn(disconnectGoogleCalendar)

  // `null` while the status is still being probed: the button and the switch both depend on it,
  // and guessing "not connected" would flash the wrong state on every load.
  const [connected, setConnected] = useState<boolean | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    void statusFn()
      .then((status) => {
        if (!cancelled) setConnected(status.connected)
      })
      .catch(() => {
        if (!cancelled) setConnected(false)
      })
    return () => {
      cancelled = true
    }
  }, [statusFn])

  async function connect() {
    setBusy(true)
    try {
      // Resolves into a redirect to Google; the Better-Auth client follows `data.url` itself.
      const { error } = await authClient.linkSocial({
        provider: 'google',
        scopes: CALENDAR_SCOPES,
        callbackURL,
      })
      if (error) toast.error(m.booking_google_error())
    } catch {
      toast.error(m.booking_google_error())
    } finally {
      setBusy(false)
    }
  }

  async function disconnect() {
    setBusy(true)
    try {
      await disconnectFn()
      onSyncChange(false)
      toast.success(m.booking_google_disconnected_toast())
    } catch {
      toast.error(m.booking_google_error())
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start gap-3">
        <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground">
          {connected ? (
            <CalendarCheck aria-hidden="true" className="size-4" />
          ) : (
            <CalendarSync aria-hidden="true" className="size-4" />
          )}
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <p className="text-sm font-medium">{m.booking_google_title()}</p>
          <p className="text-sm text-pretty text-muted-foreground">
            {connected === null
              ? m.booking_google_checking()
              : !googleEnabled
                ? m.booking_google_unavailable()
                : connected
                  ? m.booking_google_desc_connected()
                  : m.booking_google_desc_disconnected()}
          </p>
        </div>
        {googleEnabled && connected !== null && (
          <Button
            type="button"
            size="sm"
            variant={connected ? 'ghost' : 'outline'}
            disabled={busy}
            className="shrink-0"
            onClick={() => void (connected ? disconnect() : connect())}
          >
            {connected ? m.booking_google_disconnect() : m.booking_google_connect()}
          </Button>
        )}
      </div>

      <div className="flex items-start gap-3 border-t border-border pt-4">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <Label
            htmlFor="booking-google-sync"
            className={connected ? 'cursor-pointer' : 'cursor-not-allowed text-muted-foreground'}
          >
            {m.booking_google_sync_label()}
          </Label>
        </div>
        <Switch
          id="booking-google-sync"
          checked={googleSync && connected === true}
          disabled={connected !== true}
          onCheckedChange={onSyncChange}
          className="mt-0.5"
        />
      </div>
    </div>
  )
}
