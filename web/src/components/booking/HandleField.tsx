import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Button } from '#/components/ui/button'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { LIMITS, handleSchema } from '#/server/bookings/schemas'

/** `https://whenweall.com` → `whenweall.com/book/` — the bit that is fixed, whatever the deployment. */
export function bookingPrefix(appUrl: string): string {
  try {
    return `${new URL(appUrl).host}/book/`
  } catch {
    return 'whenweall.com/book/'
  }
}

/**
 * The organiser's public handle: the `whenweall.com/book/<handle>` half of every booking link. Kept
 * free of server imports so it can be unit-tested; the settings route hands it the `setHandle`
 * server function as `onSave`.
 */
export function HandleField({
  currentHandle,
  appUrl,
  onSave,
}: {
  currentHandle: string | null
  appUrl: string
  onSave: (handle: string) => Promise<void>
}) {
  const [value, setValue] = useState(currentHandle ?? '')
  const [saving, setSaving] = useState(false)
  const [serverError, setServerError] = useState<string | null>(null)

  const prefix = bookingPrefix(appUrl)
  const valid = handleSchema.safeParse(value).success
  const showFormatError = value.length > 0 && !valid
  const unchanged = value === (currentHandle ?? '')

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!valid || unchanged || saving) return

    setSaving(true)
    setServerError(null)
    try {
      await onSave(value)
      toast.success(m.settings_handle_saved())
    } catch (error) {
      if (errorCode(error) === 'HANDLE_TAKEN') setServerError(m.settings_handle_taken())
      else toast.error(m.booking_editor_error_generic())
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div>
        <h2 className="text-sm font-semibold">{m.settings_handle_title()}</h2>
        <p className="text-sm text-muted-foreground">{m.settings_handle_subtitle()}</p>
      </div>

      <form
        onSubmit={(event) => void handleSubmit(event)}
        className="flex flex-col gap-3 sm:flex-row sm:items-end"
      >
        <div className="flex flex-1 flex-col gap-1.5">
          <Label htmlFor="settings-handle">{m.settings_handle_label()}</Label>
          <div className="flex items-center rounded-md border border-input bg-transparent shadow-xs focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50 dark:bg-input/30">
            <span className="shrink-0 pl-3 text-sm text-muted-foreground select-none">
              {prefix}
            </span>
            <Input
              id="settings-handle"
              value={value}
              spellCheck={false}
              autoCapitalize="none"
              autoComplete="off"
              placeholder={m.settings_handle_placeholder()}
              maxLength={LIMITS.handleMax}
              aria-invalid={showFormatError || serverError !== null || undefined}
              aria-describedby="settings-handle-hint"
              onChange={(event) => {
                setValue(event.target.value.toLowerCase())
                setServerError(null)
              }}
              className="border-0 bg-transparent pl-0.5 shadow-none focus-visible:border-0 focus-visible:ring-0 dark:bg-transparent"
            />
          </div>
        </div>
        <Button type="submit" disabled={saving || !valid || unchanged}>
          {m.settings_handle_save()}
        </Button>
      </form>

      <p
        id="settings-handle-hint"
        className={cn(
          'text-sm',
          showFormatError || serverError ? 'text-destructive' : 'text-muted-foreground',
        )}
      >
        {serverError ??
          (valid
            ? m.settings_handle_current({ url: `${prefix}${value}` })
            : m.settings_handle_invalid())}
      </p>
    </div>
  )
}
