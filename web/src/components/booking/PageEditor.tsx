import { useEffect, useMemo, useReducer, useState, type ChangeEvent } from 'react'
import { Link, useNavigate, useRouter } from '@tanstack/react-router'
import { Save, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { AvailabilityEditor } from '#/components/booking/AvailabilityEditor'
import { DateOverridesEditor } from '#/components/booking/DateOverridesEditor'
import { SlotPreview } from '#/components/booking/SlotPreview'
import { bookingPrefix } from '#/components/booking/HandleField'
import {
  canSave,
  draftFromPage,
  draftIssues,
  draftToInput,
  draftToUpdate,
  editorReducer,
  initialDraft,
  type EditorDraft,
} from '#/components/booking/editor-state'
import { Button } from '#/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '#/components/ui/dialog'
import { Input } from '#/components/ui/input'
import { Label } from '#/components/ui/label'
import { Switch } from '#/components/ui/switch'
import { Textarea } from '#/components/ui/textarea'
import { errorCode } from '#/lib/errors'
import { m } from '#/lib/i18n'
import { cn } from '#/lib/utils'
import { createBookingPage, deleteBookingPage, LIMITS, updateBookingPage } from '#/api/bookings'
import type { PageView } from '#/api/types'

const DURATION_PRESETS = [15, 30, 45, 60, 90, 120]

/** "Intro call" → "intro-call": what the slug field pre-fills with until it is edited by hand. */
export function slugify(title: string): string {
  return title
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, LIMITS.slugMax)
    .replace(/-+$/g, '')
}

/** A bounded integer field that keeps its own draft text, so a half-typed "1" in a 15..480 field
 * doesn't get yanked back to the last valid value mid-keystroke. */
function NumberField({
  id,
  label,
  hint,
  value,
  min,
  max,
  onChange,
  className,
}: {
  id: string
  label: string
  hint?: string
  value: number
  min: number
  max: number
  onChange: (value: number) => void
  className?: string
}) {
  const [text, setText] = useState(String(value))
  const [synced, setSynced] = useState(value)
  // Adjusted during render (not in an effect), the same pattern as `CapacityField`: the visible
  // text follows a value changed from outside without an extra render pass.
  if (value !== synced) {
    setSynced(value)
    setText(String(value))
  }

  function handleChange(event: ChangeEvent<HTMLInputElement>) {
    setText(event.target.value)
    const parsed = Number(event.target.value)
    if (Number.isInteger(parsed) && parsed >= min && parsed <= max) onChange(parsed)
  }

  return (
    <div className={cn('flex flex-col gap-1.5', className)}>
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="number"
        inputMode="numeric"
        min={min}
        max={max}
        step={1}
        value={text}
        onChange={handleChange}
        onBlur={() => setText(String(value))}
        className="h-9 w-28 tabular-nums"
      />
      {hint && <p className="text-sm text-muted-foreground">{hint}</p>}
    </div>
  )
}

function Section({
  title,
  children,
  className,
}: {
  title: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <section className={cn('surface flex flex-col gap-5 p-4 sm:p-6', className)}>
      <h2 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</h2>
      {children}
    </section>
  )
}

/**
 * The booking-page editor, in both modes: `page === null` creates one, otherwise it edits that
 * page. The draft lives in a reducer (`editor-state.ts`) that owns every rule about what a valid
 * page looks like, so this component only has to lay the sections out and talk to the server.
 */
export function PageEditor({
  page,
  handle,
  appUrl,
}: {
  page: PageView | null
  handle: string | null
  appUrl: string
}) {
  const navigate = useNavigate()
  const router = useRouter()

  const isCreate = page === null
  const [draft, dispatch] = useReducer(editorReducer, page, (initial): EditorDraft =>
    initial ? draftFromPage(initial) : initialDraft('UTC'),
  )
  const [slugTouched, setSlugTouched] = useState(!isCreate)
  const [slugError, setSlugError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [customDuration, setCustomDuration] = useState(
    !DURATION_PRESETS.includes(page?.slotDurationMin ?? 30),
  )

  // The browser's zone is only knowable on the client: reading it during render would make the
  // server and the first client render disagree. A page being edited already has its own zone.
  useEffect(() => {
    if (!isCreate) return
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
    if (zone) dispatch({ type: 'setField', field: 'timezone', value: zone })
  }, [isCreate])

  const issues = useMemo(() => draftIssues(draft), [draft])
  const saveable = canSave(draft)
  const prefix = bookingPrefix(appUrl)
  const publicUrl = `${prefix}${handle ?? ''}/${draft.slug}`

  function setField(field: keyof EditorDraft, value: unknown) {
    dispatch({ type: 'setField', field, value })
  }

  function handleTitleChange(value: string) {
    setField('title', value)
    if (!slugTouched) setField('slug', slugify(value))
  }

  async function handleSave() {
    setSlugError(null)
    setSubmitting(true)
    try {
      if (isCreate) {
        const input = draftToInput(draft)
        if (!input) return
        const { id } = await createBookingPage(input)
        toast.success(m.booking_editor_created())
        await navigate({ to: '/bookings/$id', params: { id } })
        return
      }

      const payload = draftToUpdate(draft, page.id)
      if (!payload) return
      await updateBookingPage(payload)
      toast.success(m.booking_editor_updated())
      await router.invalidate()
    } catch (error) {
      if (errorCode(error) === 'slug_taken') setSlugError(m.booking_editor_slug_taken())
      else toast.error(m.booking_editor_error_generic())
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDelete() {
    if (isCreate) return
    setSubmitting(true)
    try {
      await deleteBookingPage(page.id)
      setDeleteOpen(false)
      toast.success(m.booking_editor_deleted())
      await navigate({ to: '/bookings' })
    } catch {
      toast.error(m.booking_editor_error_generic())
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div
      data-testid="page-editor"
      className="mx-auto flex w-full max-w-3xl flex-col gap-6 px-5 py-10 sm:py-14"
    >
      <header className="flex flex-col gap-1">
        <h1 className="display text-3xl">
          {isCreate ? m.booking_editor_new_title() : m.booking_editor_edit_title()}
        </h1>
        <p className="text-sm text-muted-foreground">
          {isCreate ? m.booking_editor_new_subtitle() : m.booking_editor_edit_subtitle()}
        </p>
      </header>

      <Section title={m.booking_editor_section_basics()}>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="booking-title">{m.booking_editor_title_label()}</Label>
          <Input
            id="booking-title"
            value={draft.title}
            maxLength={LIMITS.title}
            placeholder={m.booking_editor_title_placeholder()}
            onChange={(event) => handleTitleChange(event.target.value)}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="booking-slug">{m.booking_editor_slug_label()}</Label>
          <Input
            id="booking-slug"
            value={draft.slug}
            maxLength={LIMITS.slugMax}
            spellCheck={false}
            autoCapitalize="none"
            aria-invalid={slugError !== null || undefined}
            aria-describedby="booking-slug-hint"
            onChange={(event) => {
              setSlugTouched(true)
              setSlugError(null)
              setField('slug', event.target.value.toLowerCase())
            }}
            className="font-mono text-sm"
          />
          <p
            id="booking-slug-hint"
            className={cn('text-sm', slugError ? 'text-destructive' : 'text-muted-foreground')}
          >
            {slugError ??
              (handle === null ? (
                <>
                  {m.booking_page_link_needs_handle()}{' '}
                  <Link to="/settings" className="underline underline-offset-4">
                    {m.nav_settings()}
                  </Link>
                </>
              ) : (
                publicUrl
              ))}
          </p>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="booking-description">{m.booking_editor_description_label()}</Label>
          <Textarea
            id="booking-description"
            value={draft.description}
            maxLength={LIMITS.description}
            rows={3}
            placeholder={m.booking_editor_description_placeholder()}
            onChange={(event) => setField('description', event.target.value)}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="booking-location">{m.booking_editor_location_label()}</Label>
          <Input
            id="booking-location"
            value={draft.location}
            maxLength={LIMITS.location}
            placeholder={m.booking_editor_location_placeholder()}
            onChange={(event) => setField('location', event.target.value)}
          />
        </div>
      </Section>

      <Section title={m.booking_editor_section_duration()}>
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="booking-duration">{m.booking_editor_duration_label()}</Label>
            <select
              id="booking-duration"
              value={customDuration ? 'custom' : String(draft.slotDurationMin)}
              onChange={(event) => {
                if (event.target.value === 'custom') {
                  setCustomDuration(true)
                  return
                }
                setCustomDuration(false)
                setField('slotDurationMin', Number(event.target.value))
              }}
              className="focus-visible:border-ring focus-visible:ring-ring/50 h-9 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none focus-visible:ring-[3px] dark:bg-input/30"
            >
              {DURATION_PRESETS.map((minutes) => (
                <option key={minutes} value={minutes}>
                  {m.booking_editor_duration_minutes({ count: minutes })}
                </option>
              ))}
              <option value="custom">{m.booking_editor_duration_custom()}</option>
            </select>
          </div>

          {customDuration && (
            <NumberField
              id="booking-duration-custom"
              label={m.booking_editor_duration_custom_label()}
              value={draft.slotDurationMin}
              min={LIMITS.slotDurationMin}
              max={LIMITS.slotDurationMax}
              onChange={(value) => setField('slotDurationMin', value)}
            />
          )}
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <NumberField
            id="booking-buffer-before"
            label={m.booking_editor_buffer_before_label()}
            value={draft.bufferBeforeMin}
            min={LIMITS.bufferMin}
            max={LIMITS.bufferMax}
            onChange={(value) => setField('bufferBeforeMin', value)}
          />
          <NumberField
            id="booking-buffer-after"
            label={m.booking_editor_buffer_after_label()}
            hint={m.booking_editor_buffer_hint()}
            value={draft.bufferAfterMin}
            min={LIMITS.bufferMin}
            max={LIMITS.bufferMax}
            onChange={(value) => setField('bufferAfterMin', value)}
          />
          <NumberField
            id="booking-notice"
            label={m.booking_editor_notice_label()}
            hint={m.booking_editor_notice_hint()}
            value={draft.minNoticeMin}
            min={LIMITS.minNoticeMin}
            max={LIMITS.minNoticeMax}
            onChange={(value) => setField('minNoticeMin', value)}
          />
          <NumberField
            id="booking-horizon"
            label={m.booking_editor_horizon_label()}
            hint={m.booking_editor_horizon_hint()}
            value={draft.maxDaysAhead}
            min={LIMITS.maxDaysAheadMin}
            max={LIMITS.maxDaysAheadMax}
            onChange={(value) => setField('maxDaysAhead', value)}
          />
        </div>
      </Section>

      <Section title={m.booking_editor_section_availability()}>
        <AvailabilityEditor
          availability={draft.availability}
          issues={issues}
          timezone={draft.timezone}
          dispatch={dispatch}
        />
        <SlotPreview draft={draft} />
      </Section>

      <Section title={m.booking_editor_section_overrides()}>
        <DateOverridesEditor overrides={draft.dateOverrides} issues={issues} dispatch={dispatch} />
      </Section>

      <Section title={m.booking_editor_section_notifications()}>
        <div className="flex items-start gap-3">
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <Label htmlFor="booking-reminders" className="cursor-pointer">
              {m.booking_reminders_label()}
            </Label>
            <p className="text-sm text-muted-foreground">{m.booking_reminders_hint()}</p>
          </div>
          <Switch
            id="booking-reminders"
            checked={draft.reminders}
            onCheckedChange={(next) => setField('reminders', next)}
            className="mt-1"
          />
        </div>
      </Section>

      {!isCreate && (
        <Section title={m.booking_editor_section_status()}>
          <div className="flex items-start gap-3">
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <Label htmlFor="booking-status" className="cursor-pointer">
                {m.booking_editor_status_label()}
              </Label>
              <p className="text-sm text-muted-foreground">
                {draft.status === 'active'
                  ? m.booking_editor_status_hint_active()
                  : m.booking_editor_status_hint_paused()}
              </p>
            </div>
            <Switch
              id="booking-status"
              checked={draft.status === 'active'}
              onCheckedChange={(next) => setField('status', next ? 'active' : 'paused')}
              className="mt-1"
            />
          </div>
        </Section>
      )}

      <div className="flex flex-wrap items-center justify-end gap-3">
        {!saveable && (
          <p className="mr-auto text-sm text-destructive">{m.booking_editor_invalid()}</p>
        )}
        {!isCreate && (
          <Button
            type="button"
            variant="ghost"
            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={() => setDeleteOpen(true)}
          >
            <Trash2 aria-hidden="true" />
            {m.booking_editor_delete()}
          </Button>
        )}
        <Button type="button" disabled={submitting || !saveable} onClick={() => void handleSave()}>
          <Save aria-hidden="true" />
          {isCreate
            ? submitting
              ? m.booking_editor_creating()
              : m.booking_editor_create()
            : submitting
              ? m.booking_editor_saving()
              : m.booking_editor_save()}
        </Button>
      </div>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{m.booking_editor_delete_title()}</DialogTitle>
            <DialogDescription>{m.booking_editor_delete_body()}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => setDeleteOpen(false)}>
              {m.common_cancel()}
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={submitting}
              onClick={() => void handleDelete()}
            >
              {m.booking_editor_delete_confirm()}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
